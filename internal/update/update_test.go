package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creativeprojects/go-selfupdate"
)

// fakeSource stands in for GitHub: the releases it lists, how it fails, and
// what each asset's download returns.
type fakeSource struct {
	releases []selfupdate.SourceRelease
	err      error            // ListReleases fails with it
	hang     bool             // ListReleases waits for the context: a network that swallows packets
	assets   map[int64][]byte // what the download of each asset (by ID) returns
	calls    atomic.Int32
}

func (s *fakeSource) ListReleases(ctx context.Context, _ selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	s.calls.Add(1)
	if s.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.releases, nil
}

func (s *fakeSource) DownloadReleaseAsset(_ context.Context, _ *selfupdate.Release, id int64) (io.ReadCloser, error) {
	data, ok := s.assets[id]
	if !ok {
		return nil, fmt.Errorf("no asset %d", id)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type fakeAsset struct {
	id   int64
	name string
}

func (a fakeAsset) GetID() int64                  { return a.id }
func (a fakeAsset) GetName() string               { return a.name }
func (a fakeAsset) GetSize() int                  { return 3 }
func (a fakeAsset) GetBrowserDownloadURL() string { return "https://example.test/" + a.name }

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	assets     []selfupdate.SourceAsset
}

func (r fakeRelease) GetID() int64              { return 1 }
func (r fakeRelease) GetTagName() string        { return r.tag }
func (r fakeRelease) GetDraft() bool            { return r.draft }
func (r fakeRelease) GetPrerelease() bool       { return r.prerelease }
func (r fakeRelease) GetPublishedAt() time.Time { return time.Date(2026, 9, 15, 20, 3, 0, 0, time.UTC) }
func (r fakeRelease) GetReleaseNotes() string   { return "notes" }
func (r fakeRelease) GetName() string           { return r.tag }
func (r fakeRelease) GetURL() string {
	return "https://github.com/giantswarm/muster/releases/tag/" + r.tag
}
func (r fakeRelease) GetAssets() []selfupdate.SourceAsset { return r.assets }

// The running binary and the release the fake GitHub carries in most tests.
const (
	running = "v5.23.4"
	latestV = "v5.23.5"
)

// binaryName is the asset architect publishes for this platform.
func binaryName() string {
	name := "muster-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// The asset IDs of a release(): the bundle and the binary.
const (
	bundleID int64 = 1
	binaryID int64 = 2
)

// release is a GitHub release the way architect publishes muster's: one
// binary per platform next to its cosign bundle -- the bundle first, to prove
// the binary is the one picked.
func release(tag string) fakeRelease {
	return fakeRelease{tag: tag, assets: []selfupdate.SourceAsset{
		fakeAsset{bundleID, binaryName() + ".bundle"},
		fakeAsset{binaryID, binaryName()},
	}}
}

// unsignedRelease is a release with the binary for this platform but no
// bundle next to it: what a release made outside architect would look like.
func unsignedRelease(tag string) fakeRelease {
	return fakeRelease{tag: tag, assets: []selfupdate.SourceAsset{fakeAsset{binaryID, binaryName()}}}
}

// acceptingValidator stands in for the cosign validator on the happy path:
// it asks for the same bundle and accepts whatever it is handed, recording
// what that was. The signature check itself (a bundle that verifies, a
// tampered binary, a bundle for another repository) is tested where it
// lives, in github.com/giantswarm/selfupdate-cosign; the tests here prove
// that self-update wires it in so that nothing unverified reaches the disk.
type acceptingValidator struct {
	asset, bundleName string
	binary, bundle    []byte
}

func (v *acceptingValidator) GetValidationAssetName(assetName string) string {
	return assetName + ".bundle"
}
func (v *acceptingValidator) Validate(assetName string, release, validation []byte) error {
	v.asset, v.bundleName, v.binary, v.bundle = assetName, v.GetValidationAssetName(assetName), release, validation
	return nil
}

// fixture is a muster with src as GitHub, its own cache directory, a clock the
// test moves and the given running version.
type fixture struct {
	src *fakeSource
	dir string
	at  time.Time
}

func setup(t *testing.T, current string, src *fakeSource) *fixture {
	t.Helper()
	f := &fixture{src: src, dir: t.TempDir(), at: time.Date(2026, 9, 15, 21, 0, 0, 0, time.UTC)}
	t.Setenv(OptOutEnv, "")
	prevSource, prevValidator, prevDir, prevNow, prevVersion, prevExe, prevTimeout := newSource, newValidator, cacheDir, now, currentVersion, executable, remindTimeout
	newSource = func() selfupdate.Source { return src }
	cacheDir = func() string { return f.dir }
	now = func() time.Time { return f.at }
	currentVersion = func() string { return current }
	t.Cleanup(func() {
		newSource, newValidator, cacheDir, now, currentVersion, executable, remindTimeout = prevSource, prevValidator, prevDir, prevNow, prevVersion, prevExe, prevTimeout
	})
	return f
}

// installed points self-update at a throwaway file instead of the running
// executable and returns its path and content, so a test can assert that the
// file was replaced -- or that it survived a refusal byte for byte.
func installed(t *testing.T) (string, []byte) {
	t.Helper()
	content := []byte("the muster that is installed right now")
	exe := filepath.Join(t.TempDir(), binaryName())
	if err := os.WriteFile(exe, content, 0o755); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	executable = func() (string, error) { return exe, nil }
	return exe, content
}

func assertUnchanged(t *testing.T, exe string, content []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Clean(exe))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("the installed binary was replaced: %q", got)
	}
}

func (f *fixture) advance(d time.Duration) { f.at = f.at.Add(d) }

func (f *fixture) cached(t *testing.T) cache {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, cacheFile))
	if err != nil {
		t.Fatalf("reading the cache: %v", err)
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("cache is not JSON: %v\n%s", err, data)
	}
	return c
}

func remind(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	Remind(context.Background(), &out)
	return out.String()
}

func TestNewerReadsTheVersionsProjectReports(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{latestV, running, true},
		{latestV, latestV, false},
		{latestV, "v5.23.5+dirty", false},                         // a tag with local edits is the tag
		{latestV, "v5.23.6-0.20260915195350-977012d023ba", false}, // a go build after the tag
		{latestV, "v5.23.5-0.20260915190000-ba86c3b1d2e3", true},  // a go build before the tag
		{latestV, "v5.23.4-1-gddb5cbfd", true},                    // a CI branch build (git describe) after v5.23.4
		{"v6.0.0", latestV, true},
		{latestV, "dev", false},
		{latestV, "e6c760a32b485c574f91cf9b89060dfa168923c9", false},
		{"", running, false},
	} {
		if got := newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestRemindHintsAtANewerRelease(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{
		release("v5.23.3"),
		release(latestV),
		fakeRelease{tag: "v5.24.0-rc.1", prerelease: true, assets: release("v5.24.0-rc.1").assets},
		fakeRelease{tag: "v5.25.0", draft: true, assets: release("v5.25.0").assets},
	}})
	out := remind(t)
	for _, want := range []string{latestV, running, "muster self-update", OptOutEnv} {
		if !strings.Contains(out, want) {
			t.Errorf("hint lacks %q:\n%s", want, out)
		}
	}
	for _, skipped := range []string{"v5.24.0-rc.1", "v5.25.0"} {
		if strings.Contains(out, skipped) {
			t.Errorf("hint names the pre-release or draft %s:\n%s", skipped, out)
		}
	}
	if c := f.cached(t); c.Latest != latestV || !c.CheckedAt.Equal(f.at) || !c.FailedAt.IsZero() {
		t.Errorf("cache after the hint: %+v", c)
	}
}

func TestRemindStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		current  string
		releases []selfupdate.SourceRelease
		optOut   string
		asks     int32
	}{
		{name: "this is the latest", current: latestV, releases: []selfupdate.SourceRelease{release(latestV)}, asks: 1},
		{name: "a build after the tag", current: "v5.23.6-0.20260915195350-977012d023ba", releases: []selfupdate.SourceRelease{release(latestV)}, asks: 1},
		{name: "the tag with local edits", current: "v5.23.5+dirty", releases: []selfupdate.SourceRelease{release(latestV)}, asks: 1},
		{name: "no binary for this platform", current: running, releases: []selfupdate.SourceRelease{
			fakeRelease{tag: latestV, assets: []selfupdate.SourceAsset{fakeAsset{1, "muster-plan9-mips.bundle"}}},
		}, asks: 1},
		{name: "a development build never asks", current: "dev", releases: []selfupdate.SourceRelease{release(latestV)}, asks: 0},
		{name: "opted out never asks", current: running, releases: []selfupdate.SourceRelease{release(latestV)}, optOut: "1", asks: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, tc.current, &fakeSource{releases: tc.releases})
			if tc.optOut != "" {
				t.Setenv(OptOutEnv, tc.optOut)
			}
			if out := remind(t); out != "" {
				t.Errorf("unexpected hint:\n%s", out)
			}
			if got := f.src.calls.Load(); got != tc.asks {
				t.Errorf("asked GitHub %d times, want %d", got, tc.asks)
			}
		})
	}
}

func TestRemindAsksGitHubOnceAnHour(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	for i := 0; i < 3; i++ {
		if out := remind(t); !strings.Contains(out, latestV) {
			t.Fatalf("run %d: no hint:\n%s", i, out)
		}
		f.advance(20 * time.Minute)
	}
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times within the hour, want 1", got)
	}
	f.advance(time.Minute) // 61 minutes after the fetch
	remind(t)
	if got := f.src.calls.Load(); got != 2 {
		t.Errorf("asked GitHub %d times after the hour, want 2", got)
	}
}

func TestRemindGivesUpFastWhenOffline(t *testing.T) {
	f := setup(t, running, &fakeSource{hang: true})
	remindTimeout = 100 * time.Millisecond

	start := time.Now()
	out := remind(t)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the hint held the command for %s", elapsed)
	}
	if out != "" {
		t.Errorf("hint without an answer:\n%s", out)
	}
	if c := f.cached(t); !c.FailedAt.Equal(f.at) || c.Latest != "" || !c.CheckedAt.IsZero() {
		t.Errorf("cache after the failed attempt: %+v", c)
	}

	// The failure is remembered: the next commands do not wait at all.
	f.advance(5 * time.Minute)
	remind(t)
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times within the retry window, want 1", got)
	}
	f.advance(6 * time.Minute) // 11 minutes after the failure
	remind(t)
	if got := f.src.calls.Load(); got != 2 {
		t.Errorf("asked GitHub %d times after the retry window, want 2", got)
	}
}

func TestRemindKeepsTheLastAnswerWhileOffline(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	if out := remind(t); !strings.Contains(out, latestV) {
		t.Fatalf("no hint while online:\n%s", out)
	}

	f.advance(2 * time.Hour)
	f.src.err = errors.New("dial tcp: no route to host")
	if out := remind(t); !strings.Contains(out, latestV) {
		t.Errorf("the remembered answer was dropped when GitHub failed:\n%s", out)
	}
	if got := f.src.calls.Load(); got != 2 {
		t.Errorf("asked GitHub %d times, want 2", got)
	}
	if c := f.cached(t); c.Latest != latestV || !c.FailedAt.Equal(f.at) {
		t.Errorf("cache after the failed refresh: %+v", c)
	}
}

func TestRemindSurvivesAGarbledCache(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	if err := os.WriteFile(filepath.Join(f.dir, cacheFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := remind(t); !strings.Contains(out, latestV) {
		t.Errorf("no hint over a garbled cache:\n%s", out)
	}
	if c := f.cached(t); c.Latest != latestV {
		t.Errorf("cache was not rewritten: %+v", c)
	}
}

func TestRemindWorksWithoutACacheDirectory(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	cacheDir = func() string { return "" }
	for i := 0; i < 2; i++ {
		if out := remind(t); !strings.Contains(out, latestV) {
			t.Errorf("run %d: no hint:\n%s", i, out)
		}
	}
	if got := f.src.calls.Load(); got != 2 {
		t.Errorf("asked GitHub %d times without a cache, want 2", got)
	}
}

func TestRunCheckReportsANewerRelease(t *testing.T) {
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	var out bytes.Buffer
	err := Run(context.Background(), &out, true)
	if !errors.Is(err, ErrOutdated) {
		t.Fatalf("Run(--check) = %v, want ErrOutdated", err)
	}
	for _, want := range []string{running, latestV, "releases/tag/" + latestV} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if c := f.cached(t); c.Latest != latestV {
		t.Errorf("the check did not refresh the cache: %+v", c)
	}
	// The hint agrees without asking again.
	remind(t)
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times, want 1", got)
	}
}

func TestRunCheckSaysWhenNothingIsNewer(t *testing.T) {
	setup(t, latestV, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
	var out bytes.Buffer
	if err := Run(context.Background(), &out, true); err != nil {
		t.Fatalf("Run(--check) = %v", err)
	}
	if !strings.Contains(out.String(), "Nothing newer") {
		t.Errorf("report:\n%s", out.String())
	}
}

// The release binaries used to report a pseudo-version derived from an old v1
// tag, so every release was "newer" and self-update re-installed the running
// version each time. A binary that is the latest release is left alone.
func TestRunLeavesTheLatestReleaseAlone(t *testing.T) {
	f := setup(t, latestV, &fakeSource{
		releases: []selfupdate.SourceRelease{release(latestV)},
		assets:   map[int64][]byte{binaryID: []byte("the same muster again"), bundleID: []byte("{}")},
	})
	exe, content := installed(t)
	var out bytes.Buffer
	if err := Run(context.Background(), &out, false); err != nil {
		t.Fatalf("an up-to-date binary must not fail: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing newer than "+latestV) {
		t.Errorf("expected the binary to be reported as current, got:\n%s", out.String())
	}
	assertUnchanged(t, exe, content)
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times, want 1", got)
	}
}

// A build without any version ("dev") and a build that names only its commit
// are no releases: neither compares with one, and neither was installed from
// a release to begin with.
func TestRunRefusesADevelopmentBuild(t *testing.T) {
	for _, current := range []string{"dev", "e6c760a32b485c574f91cf9b89060dfa168923c9", ""} {
		t.Run(current, func(t *testing.T) {
			f := setup(t, current, &fakeSource{releases: []selfupdate.SourceRelease{release(latestV)}})
			err := Run(context.Background(), io.Discard, false)
			if err == nil || !strings.Contains(err.Error(), "development build") || !strings.Contains(err.Error(), "go install github.com/giantswarm/muster/v5@latest") {
				t.Errorf("Run() = %v, want a refusal that says how to install a release", err)
			}
			if got := f.src.calls.Load(); got != 0 {
				t.Errorf("asked GitHub %d times for a dev build", got)
			}
		})
	}
}

func TestRunReportsAnUnreachableGitHub(t *testing.T) {
	setup(t, running, &fakeSource{err: errors.New("dial tcp: no route to host")})
	err := Run(context.Background(), io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "no route to host") || !strings.Contains(err.Error(), "online") {
		t.Errorf("Run() = %v, want the network error and the hint", err)
	}
}

func TestRunReplacesTheExecutableOnceTheBundleVerifies(t *testing.T) {
	f := setup(t, running, &fakeSource{
		releases: []selfupdate.SourceRelease{release(latestV)},
		assets:   map[int64][]byte{binaryID: []byte("new"), bundleID: []byte("its bundle")},
	})
	exe, _ := installed(t)
	if err := os.Chmod(exe, 0o750); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	v := &acceptingValidator{}
	newValidator = func() selfupdate.Validator { return v }

	var out bytes.Buffer
	if err := Run(context.Background(), &out, false); err != nil {
		t.Fatalf("Run() = %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(filepath.Clean(exe))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("executable holds %q after the update", got)
	}
	if runtime.GOOS != "windows" {
		// The file keeps its mode, and the update leaves nothing beside it.
		info, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o750 {
			t.Errorf("executable has mode %v, want it to keep -rwxr-x---", mode)
		}
		entries, err := os.ReadDir(filepath.Dir(exe))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("%s holds %q, want only %s", filepath.Dir(exe), names, binaryName())
		}
	}
	if !strings.Contains(out.String(), "Verified the signature and updated to "+latestV) {
		t.Errorf("output:\n%s", out.String())
	}
	// The validator saw the binary that was installed and the bundle that
	// was published next to it, by the names architect gives them.
	if v.asset != binaryName() || v.bundleName != binaryName()+".bundle" {
		t.Errorf("validated %q against %q", v.asset, v.bundleName)
	}
	if string(v.binary) != "new" || string(v.bundle) != "its bundle" {
		t.Errorf("validated %q against bundle %q", v.binary, v.bundle)
	}

	// The new binary reports the release; the hint has nothing to say and
	// no reason to ask.
	currentVersion = func() string { return latestV }
	if hint := remind(t); hint != "" {
		t.Errorf("hint after the update:\n%s", hint)
	}
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times, want 1", got)
	}
}

// The signature check itself is tested in github.com/giantswarm/selfupdate-cosign.
// What follows proves that self-update refuses what the real validator cannot
// vouch for, and leaves the installed binary untouched when it does.

func TestRunRefusesAReleaseWithoutASignatureBundle(t *testing.T) {
	f := setup(t, running, &fakeSource{
		releases: []selfupdate.SourceRelease{unsignedRelease(latestV)},
		assets:   map[int64][]byte{binaryID: []byte("a newer muster, unsigned")},
	})
	exe, content := installed(t)

	var out bytes.Buffer
	err := Run(context.Background(), &out, false)
	if err == nil {
		t.Fatal("a release without a bundle must be refused")
	}
	if !strings.Contains(err.Error(), "no signature bundle") || !strings.Contains(err.Error(), binaryName()+".bundle") {
		t.Errorf("the error should say what is missing, got: %v", err)
	}
	if strings.Contains(err.Error(), "online?") {
		t.Errorf("a missing bundle is not a network problem: %v", err)
	}
	assertUnchanged(t, exe, content)
	// Refused before the release was even reported: go-selfupdate returns no
	// release with the error, so there is no tag to remember. The cache is
	// the hint's, and the hint asks on its own (without a bundle).
	if _, err := os.Stat(filepath.Join(f.dir, cacheFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refusal wrote a cache: %v", err)
	}
	if got := f.src.calls.Load(); got != 1 {
		t.Errorf("asked GitHub %d times, want 1", got)
	}
}

func TestRunCheckRefusesAReleaseWithoutASignatureBundle(t *testing.T) {
	setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{unsignedRelease(latestV)}})
	var out bytes.Buffer
	err := Run(context.Background(), &out, true)
	if err == nil || errors.Is(err, ErrOutdated) {
		t.Fatalf("Run(--check) = %v, want the refusal: self-update would install nothing from this release", err)
	}
	if !strings.Contains(err.Error(), "no signature bundle") {
		t.Errorf("the error should say what is missing, got: %v", err)
	}
}

func TestRunRefusesADownloadThatDoesNotVerify(t *testing.T) {
	f := setup(t, running, &fakeSource{
		releases: []selfupdate.SourceRelease{release(latestV)},
		assets: map[int64][]byte{
			binaryID: []byte("a newer muster"),
			bundleID: []byte("{}"), // not a Sigstore bundle
		},
	})
	exe, content := installed(t)

	var out bytes.Buffer
	err := Run(context.Background(), &out, false)
	if err == nil {
		t.Fatal("a download whose bundle does not verify must be refused")
	}
	if !strings.Contains(err.Error(), "is unchanged") || !strings.Contains(err.Error(), "is not a Sigstore bundle") {
		t.Errorf("the error should say the binary was refused and why, got: %v", err)
	}
	if !strings.Contains(out.String(), "Newer release: "+latestV) {
		t.Errorf("the newer release should have been announced before the refusal, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Verified the signature") {
		t.Errorf("a refused download must not report success:\n%s", out.String())
	}
	assertUnchanged(t, exe, content)
	// The release exists and was reported; the hint remembers it like after
	// any other look-up.
	if c := f.cached(t); c.Latest != latestV {
		t.Errorf("cache after the refusal: %+v", c)
	}
}

func TestRemindHintsAtAReleaseWithoutASignatureBundle(t *testing.T) {
	// The hint installs nothing, so a release self-update would refuse is
	// still news worth a line.
	f := setup(t, running, &fakeSource{releases: []selfupdate.SourceRelease{unsignedRelease(latestV)}})
	out := remind(t)
	if !strings.Contains(out, latestV) || !strings.Contains(out, "muster self-update") {
		t.Errorf("no hint for a release without a bundle:\n%s", out)
	}
	if c := f.cached(t); c.Latest != latestV || !c.FailedAt.IsZero() {
		t.Errorf("cache after the hint: %+v", c)
	}
}
