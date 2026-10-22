package aggregator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	configPkg "github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/pkg/logging"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

const (
	// devicePollLeaseTTL is how long a replica's poll lease outlives its
	// last renewal: a replica that stops renewing (a restart, a roll) hands
	// the code to the next replica asked about it after this long.
	devicePollLeaseTTL = 30 * time.Second

	// deviceStoreTimeout bounds a store call a poll makes on its own.
	deviceStoreTimeout = 5 * time.Second
)

// deviceSignInKey is one person's sign-in to one App.
type deviceSignInKey struct{ sub, app string }

// deviceSignIn is a poll this replica runs.
type deviceSignIn struct {
	code   *chatgptlogin.Code
	cancel context.CancelFunc
}

// deviceSignIns are the device-code sign-ins of provider Apps: at most one
// outstanding code per person and App, kept in the store every replica
// reads, polled by the one replica holding its lease. A replica asked about
// a code no replica polls takes the lease and polls it. The grant is filed
// in the shared token store, where every replica finds it.
type deviceSignIns struct {
	newClient func(configPkg.ProviderAppConfig) deviceCodeClient
	store     deviceSignInStore
	// owner names this replica in the poll leases.
	owner string
	// heartbeat paces the lease renewals of a poll. Tests replace it.
	heartbeat func() (<-chan time.Time, func())

	mu      sync.Mutex
	ctx     context.Context
	stop    context.CancelFunc
	polling map[deviceSignInKey]*deviceSignIn
	polls   sync.WaitGroup
}

func newDeviceSignIns(store deviceSignInStore) *deviceSignIns {
	ctx, stop := context.WithCancel(context.Background())
	owner := make([]byte, 8)
	_, _ = rand.Read(owner)
	return &deviceSignIns{
		newClient: newChatGPTDeviceClient,
		store:     store,
		owner:     hex.EncodeToString(owner),
		heartbeat: func() (<-chan time.Time, func()) {
			ticker := time.NewTicker(devicePollLeaseTTL / 3)
			return ticker.C, ticker.Stop
		},
		ctx:     ctx,
		stop:    stop,
		polling: make(map[deviceSignInKey]*deviceSignIn),
	}
}

func warnDeviceStore(op string, k deviceSignInKey, err error) {
	logging.WarnWithAttrs("Aggregator", "provider_app_device_store_failed",
		slog.String("op", op),
		slog.String("app", k.app),
		slog.String("subject", logging.TruncateIdentifier(k.sub)),
		slog.String("error", err.Error()))
}

// outstanding returns the person's outstanding code for the App, nil when
// none is.
func (d *deviceSignIns) outstanding(ctx context.Context, sub, app string) *chatgptlogin.Code {
	if d == nil {
		return nil
	}
	k := deviceSignInKey{sub, app}
	code, err := d.store.outstanding(ctx, k)
	if err != nil {
		warnDeviceStore("outstanding", k, err)
	}
	return code
}

// current returns the person's outstanding code for the App, nil when none
// is, and polls it here when no replica does; complete files the grant.
func (d *deviceSignIns) current(ctx context.Context, sub string, app configPkg.ProviderAppConfig, complete func(*pkgoauth.Token)) *chatgptlogin.Code {
	code := d.outstanding(ctx, sub, app.Name)
	if code != nil {
		d.ensurePoll(ctx, deviceSignInKey{sub, app.Name}, app, code, complete)
	}
	return code
}

// lastFailure returns why the person's last sign-in to the App failed.
func (d *deviceSignIns) lastFailure(ctx context.Context, sub, app string) string {
	if d == nil {
		return ""
	}
	k := deviceSignInKey{sub, app}
	reason, err := d.store.failure(ctx, k)
	if err != nil {
		warnDeviceStore("failure", k, err)
	}
	return reason
}

// fail records a sign-in's failure.
func (d *deviceSignIns) fail(sub, app string, err error) {
	if d == nil {
		return
	}
	k := deviceSignInKey{sub, app}
	ctx, done := context.WithTimeout(context.Background(), deviceStoreTimeout)
	defer done()
	if storeErr := d.store.setFailure(ctx, k, err.Error()); storeErr != nil {
		warnDeviceStore("setFailure", k, storeErr)
	}
}

// start returns the person's outstanding code, or requests one and polls
// it; complete files the resulting grant.
func (d *deviceSignIns) start(ctx context.Context, sub string, app configPkg.ProviderAppConfig, complete func(*pkgoauth.Token)) (*chatgptlogin.Code, error) {
	if d == nil {
		return nil, errors.New("device-code sign-in is not available")
	}
	if code := d.current(ctx, sub, app, complete); code != nil {
		return code, nil
	}
	if d.ctx.Err() != nil {
		return nil, errors.New("muster is shutting down")
	}
	k := deviceSignInKey{sub, app.Name}
	requested, err := d.newClient(app).RequestCode(ctx)
	if err != nil {
		d.fail(sub, app.Name, err)
		return nil, err
	}
	// A concurrent call, here or on another replica, may have issued a
	// code first: that one is the person's.
	code, err := d.store.claim(ctx, k, requested)
	if err != nil {
		return nil, err
	}
	if code == requested {
		if err := d.store.setFailure(ctx, k, ""); err != nil {
			warnDeviceStore("setFailure", k, err)
		}
	}
	d.ensurePoll(ctx, k, app, code, complete)
	return code, nil
}

// ensurePoll polls code here unless this or another replica already does.
func (d *deviceSignIns) ensurePoll(ctx context.Context, k deviceSignInKey, app configPkg.ProviderAppConfig, code *chatgptlogin.Code, complete func(*pkgoauth.Token)) {
	if d.isPolling(k, code) || d.ctx.Err() != nil {
		return
	}
	holder := d.owner + "/" + code.UserCode
	held, err := d.store.lease(ctx, k, holder, devicePollLeaseTTL)
	if err != nil {
		warnDeviceStore("lease", k, err)
		return
	}
	if !held {
		return // another replica polls it
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if current := d.polling[k]; current != nil {
		if current.code.UserCode == code.UserCode {
			return // a concurrent call started the poll
		}
		current.cancel() // a former code's
	}
	if d.ctx.Err() != nil {
		return
	}
	pollCtx, cancel := context.WithDeadline(d.ctx, code.ExpiresAt.Add(deviceCodeGrace))
	signIn := &deviceSignIn{code: code, cancel: cancel}
	d.polling[k] = signIn
	d.polls.Add(1)
	go d.poll(pollCtx, signIn, k, holder, app, complete)
}

// isPolling reports whether this replica polls code.
func (d *deviceSignIns) isPolling(k deviceSignInKey, code *chatgptlogin.Code) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.polling[k]
	return current != nil && current.code.UserCode == code.UserCode
}

// poll waits for the person to enter the code while this replica holds its
// lease, then files the grant unless the sign-in was ended meanwhile.
func (d *deviceSignIns) poll(ctx context.Context, signIn *deviceSignIn, k deviceSignInKey, holder string, app configPkg.ProviderAppConfig, complete func(*pkgoauth.Token)) {
	defer d.polls.Done()
	defer signIn.cancel()
	stopRenewing := d.renewLease(ctx, signIn, k, holder)
	token, err := d.newClient(app).Await(ctx, signIn.code)
	stopRenewing()

	d.mu.Lock()
	if d.polling[k] == signIn {
		delete(d.polling, k)
	}
	d.mu.Unlock()

	// Not cancelled with muster: the lease is given up on shutdown too.
	storeCtx, done := context.WithTimeout(context.WithoutCancel(d.ctx), deviceStoreTimeout)
	defer done()
	defer func() {
		if err := d.store.unlease(storeCtx, k, holder); err != nil {
			warnDeviceStore("unlease", k, err)
		}
	}()
	if d.ctx.Err() != nil {
		return // muster stops: the code stays for another replica
	}
	if errors.Is(err, context.Canceled) {
		return // ended by a disconnect, or another replica took the code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = chatgptlogin.ErrExpired
	}
	released, storeErr := d.store.release(storeCtx, k, signIn.code.UserCode)
	if storeErr != nil {
		warnDeviceStore("release", k, storeErr)
		return
	}
	if !released {
		return // ended by a disconnect
	}
	if err != nil {
		d.fail(k.sub, k.app, err)
		logging.WarnWithAttrs("Aggregator", "provider_app_device_sign_in_failed",
			slog.String("app", k.app),
			slog.String("subject", logging.TruncateIdentifier(k.sub)),
			slog.String("error", err.Error()))
		return
	}
	complete(token)
}

// renewLease renews the poll's lease on every heartbeat and ends the poll
// once its code is no longer outstanding (a disconnect on any replica) or
// another replica holds the lease. The returned function stops it.
func (d *deviceSignIns) renewLease(ctx context.Context, signIn *deviceSignIn, k deviceSignInKey, holder string) func() {
	beat, stopBeat := d.heartbeat()
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-quit:
				return
			case <-beat:
			}
			code, err := d.store.outstanding(ctx, k)
			if err == nil && (code == nil || code.UserCode != signIn.code.UserCode) {
				signIn.cancel()
				return
			}
			held, err := d.store.lease(ctx, k, holder, devicePollLeaseTTL)
			if err != nil {
				warnDeviceStore("lease", k, err) // a store hiccup: keep polling
				continue
			}
			if !held {
				signIn.cancel()
				return
			}
		}
	}()
	return func() {
		stopBeat()
		close(quit)
		<-done
	}
}

// cancel ends the person's outstanding sign-in to the App on every replica
// and forgets its failure.
func (d *deviceSignIns) cancel(ctx context.Context, sub, app string) {
	if d == nil {
		return
	}
	k := deviceSignInKey{sub, app}
	d.mu.Lock()
	if signIn := d.polling[k]; signIn != nil {
		signIn.cancel()
		delete(d.polling, k)
	}
	d.mu.Unlock()
	if _, err := d.store.release(ctx, k, ""); err != nil {
		warnDeviceStore("release", k, err)
	}
	if err := d.store.setFailure(ctx, k, ""); err != nil {
		warnDeviceStore("setFailure", k, err)
	}
}

// shutdown cancels every poll and waits for them to end; their codes stay
// outstanding for the other replicas.
func (d *deviceSignIns) shutdown() {
	if d == nil {
		return
	}
	d.stop()
	d.polls.Wait()
}
