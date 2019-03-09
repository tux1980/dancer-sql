package tokenrequest

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path"
	"sync"

	"github.com/RangelReale/osincli"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	"github.com/openshift/origin/pkg/oauth/urls"
	"github.com/openshift/origin/pkg/oauthserver"
	"github.com/openshift/origin/pkg/oauthserver/authenticator/password/bootstrap"
)

type endpointDetails struct {
	publicMasterURL string
	// osinOAuthClient is the private OAuth client used by this endpoint.
	// It starts out nil and is lazily initialized when this endpoint is called.
	osinOAuthClient *osincli.Client
	// osinOAuthClientGetter is used to initialize osinOAuthClient.
	// Since it can return an error, it may be called multiple times.
	osinOAuthClientGetter func() (*osincli.Client, error)
	// ready is closed to signal that osinOAuthClient is no longer nil.
	// Nothing sends on ready so <-ready only returns when it has been closed.
	ready chan struct{}
	// initLock guards reads and writes to osinOAuthClient when it could still be nil.
	initLock sync.Mutex

	// to check if we need the logout link for the bootstrap user
	tokens                v1.OAuthAccessTokenInterface
	openShiftLogoutPrefix string
}

// TODO this interface needs to be moved
type Endpoints interface {
	Install(mux oauthserver.Mux, paths ...string)
}

func NewEndpoints(publicMasterURL, openShiftLogoutPrefix string, osinOAuthClientGetter func() (*osincli.Client, error), tokens v1.OAuthAccessTokenInterface) Endpoints {
	return &endpointDetails{
		publicMasterURL:       publicMasterURL,
		osinOAuthClientGetter: osinOAuthClientGetter,
		ready:                 make(chan struct{}),
		tokens:                tokens,
		openShiftLogoutPrefix: openShiftLogoutPrefix,
	}
}

// Install registers the request token endpoints into a mux. It is expected that the
// provided prefix will serve all operations
func (e *endpointDetails) Install(mux oauthserver.Mux, paths ...string) {
	for _, prefix := range paths {
		mux.HandleFunc(path.Join(prefix, urls.RequestTokenEndpoint), e.readyHandler(e.requestToken))
		mux.HandleFunc(path.Join(prefix, urls.DisplayTokenEndpoint), e.readyHandler(e.displayToken))
		mux.HandleFunc(path.Join(prefix, urls.ImplicitTokenEndpoint), e.implicitToken)
	}
}

// TODO we may want to start doing live lookups for this endpoint
func (e *endpointDetails) readyHandler(delegate func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, h *http.Request) {
		select {
		case <-e.ready:
		default:
			if err := e.safeInitOsinOAuthClientOnce(); err != nil {
				utilruntime.HandleError(fmt.Errorf("failed to get Osin OAuth client for token endpoint: %v", err))
				http.Error(w, "OAuth token endpoint is not ready", http.StatusInternalServerError)
				return
			}
		}
		delegate(w, h)
	}
}

// safeInitOsinOAuthClientOnce initializes osinOAuthClient exactly once using osinOAuthClientGetter.
// It is goroutine safe, reentrant and can be safely called multiple times.
func (e *endpointDetails) safeInitOsinOAuthClientOnce() error {
	// Use a lock and nil check to make sure we never close endpoints.ready more than once
	// and that we only try to fetch osinOAuthClient until the first time we are successful
	e.initLock.Lock()
	defer e.initLock.Unlock()
	if e.osinOAuthClient == nil {
		osinOAuthClient, err := e.osinOAuthClientGetter()
		if err != nil {
			return err
		}
		e.osinOAuthClient = osinOAuthClient
		close(e.ready)
	}
	return nil
}

// requestToken works for getting a token in your browser and seeing what your token is
func (e *endpointDetails) requestToken(w http.ResponseWriter, req *http.Request) {
	authReq := e.osinOAuthClient.NewAuthorizeRequest(osincli.CODE)
	oauthURL := authReq.GetAuthorizeUrl()

	http.Redirect(w, req, oauthURL.String(), http.StatusFound)
}

func (e *endpointDetails) displayToken(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	requestURL := urls.OpenShiftOAuthTokenRequestURL("") // relative url to token request endpoint
	data := tokenData{RequestURL: requestURL, PublicMasterURL: e.publicMasterURL}

	authorizeReq := e.osinOAuthClient.NewAuthorizeRequest(osincli.CODE)
	authorizeData, err := authorizeReq.HandleRequest(req)
	if err != nil {
		data.Error = fmt.Sprintf("Error handling auth request: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		renderToken(w, data)
		return
	}

	accessReq := e.osinOAuthClient.NewAccessRequest(osincli.AUTHORIZATION_CODE, authorizeData)
	accessData, err := accessReq.GetToken()
	if err != nil {
		data.Error = fmt.Sprintf("Error getting token: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		renderToken(w, data)
		return
	}

	token, err := e.tokens.Get(accessData.AccessToken, metav1.GetOptions{})
	if err != nil {
		data.Error = "Error checking token" // do not leak error to user, do not log error
		w.WriteHeader(http.StatusInternalServerError)
		renderToken(w, data)
		return
	}

	if token.UserName == bootstrap.BootstrapUser {
		// only the bootstrap user has a session we maintain for one more than OAuth flow
		data.LogoutURL = e.openShiftLogoutPrefix
	}

	data.AccessToken = accessData.AccessToken
	renderToken(w, data)
}

func renderToken(w io.Writer, data tokenData) {
	if err := tokenTemplate.Execute(w, data); err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to render token template: %v", err))
	}
}

type tokenData struct {
	Error           string
	AccessToken     string
	RequestURL      string
	PublicMasterURL string
	LogoutURL       string
}

// TODO: allow template to be read from an external file
var tokenTemplate = template.Must(template.New("tokenTemplate").Parse(`
<style>
	body     { font-family: sans-serif; font-size: 14px; margin: 2em 2%; background-color: #F9F9F9; }
	h2       { font-size: 1.4em;}
	h3       { font-size: 1em; margin: 1.5em 0 0; }
	code,pre { font-family: Menlo, Monaco, Consolas, monospace; }
	code     { font-weight: 300; font-size: 1.5em; margin-bottom: 1em; display: inline-block;  color: #646464;  }
	pre      { padding-left: 1em; border-radius: 5px; color: #003d6e; background-color: #EAEDF0; padding: 1.5em 0 1.5em 4.5em; white-space: normal; text-indent: -2em; }
	a        { color: #00f; text-decoration: none; }
	a:hover  { text-decoration: underline; }
	button   { background: none; border: none; color: #00f; text-decoration: none; font: inherit; padding: 0; }
	button:hover { text-decoration: underline; cursor: pointer; }
	@media (min-width: 768px) {
		.nowrap { white-space: nowrap; }
	}
</style>

{{ if .Error }}
  {{ .Error }}
{{ else }}
  <h2>Your API token is</h2>
  <code>{{.AccessToken}}</code>

  <h2>Log in with this token</h2>
  <pre>oc login <span class="nowrap">--token={{.AccessToken}}</span> <span class="nowrap">--server={{.PublicMasterURL}}</span></pre>

  <h3>Use this token directly against the API</h3>
  <pre>curl <span class="nowrap">-H "Authorization: Bearer {{.AccessToken}}"</span> <span class="nowrap">"{{.PublicMasterURL}}/apis/user.openshift.io/v1/users/~"</span></pre>
{{ end }}

<br><br>
<a href="{{.RequestURL}}">Request another token</a>

{{ if .LogoutURL }}
  <br><br>
  <form method="post" action="{{.LogoutURL}}">
    <input type="hidden" name="then" value="{{.RequestURL}}">
    <button type="submit">
      Logout
    </button>
  </form>
{{ end }}
`))

func (e *endpointDetails) implicitToken(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(`
You have reached this page by following a redirect Location header from an OAuth authorize request.

If a response_type=token parameter was passed to the /authorize endpoint, that requested an
"Implicit Grant" OAuth flow (see https://tools.ietf.org/html/rfc6749#section-4.2).

That flow requires the access token to be returned in the fragment portion of a redirect header.
Rather than following the redirect here, you can obtain the access token from the Location header
(see https://tools.ietf.org/html/rfc6749#section-4.2.2):

  1. Parse the URL in the Location header and extract the fragment portion
  2. Parse the fragment using the "application/x-www-form-urlencoded" format
  3. The access_token parameter contains the granted OAuth access token
`))
}
