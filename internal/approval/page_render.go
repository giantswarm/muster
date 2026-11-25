package approval

import (
	"crypto/rand"
	"html/template"
	"math"
	"net/http"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The page's templates. html/template escapes every value: tool titles,
// argument values and labels come from a connector's definition and an
// agent's call and are shown as text, never as markup.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Heading}} · muster</title>
<style>
body{font-family:system-ui,sans-serif;max-width:44rem;margin:2rem auto;padding:0 1rem;color:#1b1f24;background:#fff}
h1{font-size:1.4rem}.lead{font-size:1.1rem}.tag{display:inline-block;padding:.1rem .5rem;border-radius:.3rem;background:#fff4e5;color:#8a4b00;font-size:.85rem}
dl{display:grid;grid-template-columns:max-content 1fr;gap:.3rem 1rem}dt{font-weight:600}dd{margin:0;white-space:pre-wrap;word-break:break-all}
code{font-family:ui-monospace,monospace;font-size:.9em}.muted{color:#57606a;font-size:.9rem}ol li.asks{font-weight:600}
.actions{display:flex;gap:1rem;margin-top:1.5rem}button{font-size:1rem;padding:.5rem 1.2rem;border-radius:.4rem;border:1px solid #8c959f;background:#f6f8fa;cursor:pointer}
button.approve{background:#1f6feb;color:#fff;border-color:#1f6feb}button[disabled]{opacity:.5;cursor:default}
pre{white-space:pre-wrap;word-break:break-all;background:#f6f8fa;padding:.5rem}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}.muted{color:#8d96a0}pre,button{background:#161b22;color:#e6edf3}.tag{background:#3d2a00;color:#f0c27b}}
</style></head><body>
<h1>{{.Heading}}</h1>
{{if .Body}}<p class="lead">{{.Body}}</p>{{end}}
{{with .Request}}
<p class="lead">{{.Display.Message}}</p>
<dl>
<dt>Action</dt><dd>{{.Display.Title}} <code>{{.Target.Name}}</code>{{if .Display.ChangesData}} <span class="tag">Changes data</span>{{end}}</dd>
{{if .Display.Connector}}<dt>Connector</dt><dd>{{.Display.Connector}}</dd>{{end}}
{{range .Display.Arguments}}<dt>{{.Label}} <code class="muted">{{.Key}}</code></dt><dd>{{.Value}}</dd>{{end}}
</dl>
{{if .Display.Changes}}<h2>What changes</h2><dl>{{range .Display.Changes}}<dt>{{.Field}}</dt><dd>{{.Old}} → {{.New}}</dd>{{end}}</dl>{{end}}
{{if .Display.Preset}}<h2>Settings the preset writes</h2><dl>{{range .Display.Preset}}<dt>{{.Label}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
{{if .Display.Steps}}<h2>Steps</h2><ol>{{range .Display.Steps}}<li{{if .Asks}} class="asks"{{end}}>{{.Label}} <code>{{.Tool}}</code>{{range .Arguments}}<br><span class="muted">{{.Label}}: {{.Value}}</span>{{end}}</li>{{end}}</ol>{{end}}
{{end}}
{{if .Request}}<p class="muted">Asks because: {{.Request.Display.Source}} setting · {{if $.Installation}}Installation {{$.Installation}} · {{end}}{{.Request.CallerKind}}{{if .Request.Agent}} · Agent {{.Request.Agent}}{{end}} · Session {{.Session}} · Expires in {{.ExpiresIn}} min</p>
<details><summary>Show details</summary><pre>{{.Request.Arguments}}</pre><p class="muted">SHA-256 {{.Request.ArgumentsHash}}</p></details>
{{if .Token}}
<form method="post" action="{{.Action}}">
<input type="hidden" name="token" value="{{.Token}}">
<div class="actions"><button name="decision" value="decline" disabled>Decline</button><button class="approve" name="decision" value="approve" disabled>Approve</button></div>
</form>
<noscript><p class="muted">The buttons need JavaScript.</p></noscript>
<script nonce="{{.ScriptNonce}}">
(function(){function arm(){setTimeout(function(){document.querySelectorAll('button[name=decision]').forEach(function(b){b.disabled=false})},1000)}
if(document.visibilityState==='visible'){arm()}else{document.addEventListener('visibilitychange',function v(){if(document.visibilityState==='visible'){document.removeEventListener('visibilitychange',v);arm()}})}})();
</script>
{{end}}{{end}}
</body></html>`))

// pageData is what the template renders.
type pageData struct {
	Heading      string
	Body         string
	Request      *api.ApprovalRequest
	Installation string
	Session      string
	ExpiresIn    int
	Action       string
	Token        string
	ScriptNonce  string
}

func (p *Page) render(w http.ResponseWriter, status int, data pageData) {
	securityHeaders(w, data.ScriptNonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pageTemplate.Execute(w, data); err != nil {
		logging.Warn("Approval", "Rendering the approval page failed: %v", err)
	}
}

// renderMessage renders a page without a request: an error or the end.
func (p *Page) renderMessage(w http.ResponseWriter, status int, heading, body string) {
	p.render(w, status, pageData{Heading: heading, Body: body})
}

// renderRequest renders a request for the browser's signed-in person: with
// the arguments and buttons for the person it is bound to, without both for
// anyone else.
func (p *Page) renderRequest(w http.ResponseWriter, request *api.ApprovalRequest, flow *api.ApprovalPageFlow) {
	if flow.Identity != request.Identity {
		p.renderMessage(w, http.StatusForbidden, "This request is someone else's",
			"It was made for "+request.Identity.Subject+" at "+request.Identity.Issuer+". Only they can decide it.")
		return
	}
	if request.Status != api.ApprovalStatusPending {
		p.renderMessage(w, http.StatusConflict, "This request was already answered", "Go back to where the request came from and continue there.")
		return
	}
	data := pageData{
		Heading:      heading(request),
		Request:      request,
		Installation: p.Installation,
		Session:      logging.TruncateIdentifier(request.SessionID),
		ExpiresIn:    int(math.Ceil(request.ExpiresAt.Sub(p.now()).Minutes())),
		Action:       p.URL(request.ID),
		Token:        flow.Token,
		ScriptNonce:  rand.Text(),
	}
	p.render(w, http.StatusOK, data)
}

// heading is the page's question for the kind of call.
func heading(request *api.ApprovalRequest) string {
	switch {
	case request.Target.Kind == api.ApprovalTargetWorkflow:
		return "Approve this workflow?"
	case len(request.Display.Changes) > 0:
		return "Approve this change?"
	default:
		return "Approve this action?"
	}
}
