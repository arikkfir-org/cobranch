package auth

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// The session cookie's names: host-only when every workload shares one host, otherwise for the hosts' nearest common
// parent domain. Both are HttpOnly, Secure and SameSite=Lax.
const (
	HostCookie   = "__Host-cobranch-session"
	SecureCookie = "__Secure-cobranch-session"
	// FlowCookie binds a sign-in flow to the browser that began it; only the Auth server reads it.
	FlowCookie = "__Host-cobranch-flow"
)

// Cookie says how the session cookie is set: its name, and its Domain attribute, "" for a host-only cookie.
type Cookie struct {
	Name   string
	Domain string
}

// CookieScope decides the session cookie from the workloads' public URLs: __Host-cobranch-session when they share one
// host, so no other host can set or read it; otherwise __Secure-cobranch-session with Domain set to their nearest
// common parent, which must be below a public suffix.
func CookieScope(urls ...*url.URL) (Cookie, error) {
	var hosts []string
	for _, u := range urls {
		if u == nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return Cookie{}, fmt.Errorf("%s has no host", u)
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		return Cookie{}, errors.New("no URL names a host")
	}
	parent := hosts[0]
	for _, host := range hosts[1:] {
		parent = commonParent(parent, host)
	}
	if parent == hosts[0] && allEqual(hosts) {
		return Cookie{Name: HostCookie}, nil
	}
	if parent == "" {
		return Cookie{}, fmt.Errorf("the hosts %s share no parent domain", strings.Join(hosts, ", "))
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(parent); err != nil {
		return Cookie{}, fmt.Errorf("the hosts %s share only %s, a public suffix: a cookie can't be set for it",
			strings.Join(hosts, ", "), parent)
	}
	return Cookie{Name: SecureCookie, Domain: parent}, nil
}

// commonParent is the longest domain both a and b are under, themselves included, or "".
func commonParent(a, b string) string {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	var common []string
	for i, j := len(as)-1, len(bs)-1; i >= 0 && j >= 0 && as[i] == bs[j]; i, j = i-1, j-1 {
		common = append([]string{as[i]}, common...)
	}
	return strings.Join(common, ".")
}

func allEqual(hosts []string) bool {
	for _, h := range hosts[1:] {
		if h != hosts[0] {
			return false
		}
	}
	return true
}
