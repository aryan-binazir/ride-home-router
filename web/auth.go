package web

import "net/url"

type AuthConfig struct {
	PublishableKey string `json:"publishableKey"`
	ScriptURL      string `json:"scriptURL"`
}

func (c AuthConfig) Origin() string {
	u, err := url.Parse(c.ScriptURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

const AuthHeadTemplate = `{{define "auth-head"}}{{if and .PublishableKey .ScriptURL}}
<meta name="rhr-auth-config" data-publishable-key="{{.PublishableKey}}" data-script-url="{{.ScriptURL}}">
{{if .Origin}}<link rel="preconnect" href="{{.Origin}}" crossorigin="anonymous">{{end}}
<link rel="preload" href="{{.ScriptURL}}" as="script" crossorigin="anonymous">
{{end}}{{end}}`
