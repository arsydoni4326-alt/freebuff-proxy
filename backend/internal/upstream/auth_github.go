package upstream

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

// --- GitHub protocol HTML helpers -------------------------------------------

// githubProtocolForm is one parsed HTML form.
type githubProtocolForm struct {
	Action string
	Method string
	Fields url.Values
}

// githubProtocolFindLoginForm finds the GitHub password login form: action
// contains "/session" with an authenticity_token, or carries login/password
// fields (reference githubProtocolFindLoginForm).
func githubProtocolFindLoginForm(body []byte) (githubProtocolForm, bool) {
	forms := githubProtocolExtractForms(body)
	for _, form := range forms {
		action := strings.ToLower(form.Action)
		if strings.Contains(action, "/session") && form.Fields.Get("authenticity_token") != "" {
			return form, true
		}
		if _, hasLogin := form.Fields["login"]; hasLogin {
			if _, hasPassword := form.Fields["password"]; hasPassword {
				return form, true
			}
		}
	}
	return githubProtocolForm{}, false
}

// githubProtocolFindTOTPForm finds the two-factor form (action contains
// "/sessions/two-factor" or an app_otp field; reference
// githubProtocolFindTOTPForm).
func githubProtocolFindTOTPForm(body []byte) (githubProtocolForm, bool) {
	forms := githubProtocolExtractForms(body)
	for _, form := range forms {
		action := strings.ToLower(form.Action)
		if strings.Contains(action, "/sessions/two-factor") {
			return form, true
		}
		if _, ok := form.Fields["app_otp"]; ok {
			return form, true
		}
	}
	return githubProtocolForm{}, false
}

// githubProtocolExtractForms parses every <form> in body into
// (action, method, hidden/input fields).
func githubProtocolExtractForms(body []byte) []githubProtocolForm {
	root, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	var forms []githubProtocolForm
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil {
			return
		}
		if n.Type == xhtml.ElementNode && strings.EqualFold(n.Data, "form") {
			f := githubProtocolForm{Action: githubProtocolAttr(n, "action"), Method: strings.ToUpper(firstOfN(githubProtocolAttr(n, "method"), http.MethodGet)), Fields: url.Values{}}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				githubProtocolCollectInputs(child, f.Fields)
			}
			forms = append(forms, f)
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return forms
}

func githubProtocolCollectInputs(n *xhtml.Node, fields url.Values) {
	if n.Type == xhtml.ElementNode && strings.EqualFold(n.Data, "input") {
		name := githubProtocolAttr(n, "name")
		typ := strings.ToLower(githubProtocolAttr(n, "type"))
		if name != "" && typ != "submit" && typ != "button" && typ != "checkbox" {
			fields.Set(name, githubProtocolAttr(n, "value"))
		}
		return
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		githubProtocolCollectInputs(child, fields)
	}
}

func githubProtocolAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func firstOfN(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// githubProtocolAuthorizeURL derives the OAuth authorize URL from the CLI
// login URL (the codebuff login page links to github.com/login/oauth/
// authorize?...auth_code=...). "" when the login URL carries no auth_code.
func githubProtocolAuthorizeURL(loginURL string) string {
	u, err := url.Parse(loginURL)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(u.Query().Get("auth_code")) == "" {
		return ""
	}
	auth := *u
	auth.Host = "github.com"
	auth.Path = "/login/oauth/authorize"
	return auth.String()
}

// githubProtocolOAuthCallbackURL finds the meta-refresh / link back to the
// codebuff callback (reference githubProtocolOAuthCallbackURL).
func githubProtocolOAuthCallbackURL(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	root, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var found string
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil || found != "" {
			return
		}
		if n.Type == xhtml.ElementNode {
			if strings.EqualFold(n.Data, "meta") && strings.EqualFold(githubProtocolAttr(n, "http-equiv"), "refresh") {
				if target := githubProtocolRefreshURL(githubProtocolAttr(n, "content")); target != "" {
					found = target
					return
				}
			}
			if strings.EqualFold(n.Data, "a") {
				if href := githubProtocolAttr(n, "href"); strings.Contains(href, "/api/auth/callback/github") {
					found = href
					return
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.TrimSpace(found)
}

// githubProtocolRefreshURL extracts the URL=... target from a
// meta http-equiv=refresh content string.
func githubProtocolRefreshURL(content string) string {
	for _, part := range strings.Split(content, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "url=") {
			return strings.Trim(strings.TrimPrefix(part[4:], "url="), `"'`)
		}
	}
	return ""
}

// githubProtocolTOTPAt computes the RFC 6238 6-digit code for secret at now
// (reference githubProtocolTOTPAt).
func githubProtocolTOTPAt(secret string, now time.Time) (string, error) {
	key, err := githubProtocolDecodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, key)
	if _, err := mac.Write(msg[:]); err != nil {
		return "", err
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (int(sum[offset])&0x7f)<<24 |
		(int(sum[offset+1])&0xff)<<16 |
		(int(sum[offset+2])&0xff)<<8 |
		(int(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", code%1_000_000), nil
}
