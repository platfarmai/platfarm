package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// wechatGet 可被测试替换，避免真实出网。
var wechatGet = http.Get

type wechatProvider struct {
	appID  string
	secret string
}

func newWeChatProvider() *wechatProvider {
	return &wechatProvider{
		appID:  envOrEmpty("WECHAT_APPID"),
		secret: envOrEmpty("WECHAT_SECRET"),
	}
}

func (p *wechatProvider) ID() string { return "wechat" }

func (p *wechatProvider) Start(state string) (string, error) {
	if p.appID == "" || p.secret == "" {
		return "", misconfig(p.ID())
	}
	q := url.Values{
		"appid":         {p.appID},
		"redirect_uri":  {selfURL() + "/api/oauth/wechat/callback"},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {state},
	}
	// 微信要求尾部带 #wechat_redirect
	return "https://open.weixin.qq.com/connect/qrconnect?" + q.Encode() + "#wechat_redirect", nil
}

func (p *wechatProvider) Callback(code string) (*ExternalProfile, error) {
	if p.appID == "" || p.secret == "" {
		return nil, misconfig(p.ID())
	}
	tokURL := "https://api.weixin.qq.com/sns/oauth2/access_token?" + url.Values{
		"appid":      {p.appID},
		"secret":     {p.secret},
		"code":       {code},
		"grant_type": {"authorization_code"},
	}.Encode()

	resp, err := wechatGet(tokURL)
	if err != nil {
		return nil, fmt.Errorf("wechat token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("wechat token read: %w", err)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"openid"`
		UnionID     string `json:"unionid"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("wechat token decode: %w", err)
	}
	if tok.ErrCode != 0 {
		return nil, fmt.Errorf("wechat token: errcode=%d errmsg=%s", tok.ErrCode, tok.ErrMsg)
	}
	if tok.AccessToken == "" || tok.OpenID == "" {
		return nil, fmt.Errorf("wechat token: missing access_token or openid")
	}

	infoURL := "https://api.weixin.qq.com/sns/userinfo?" + url.Values{
		"access_token": {tok.AccessToken},
		"openid":       {tok.OpenID},
	}.Encode()
	uresp, err := wechatGet(infoURL)
	if err != nil {
		return nil, fmt.Errorf("wechat userinfo: %w", err)
	}
	defer uresp.Body.Close()
	ubody, err := io.ReadAll(io.LimitReader(uresp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("wechat userinfo read: %w", err)
	}

	var u struct {
		OpenID   string `json:"openid"`
		Nickname string `json:"nickname"`
		UnionID  string `json:"unionid"`
		ErrCode  int    `json:"errcode"`
		ErrMsg   string `json:"errmsg"`
	}
	if err := json.Unmarshal(ubody, &u); err != nil {
		return nil, fmt.Errorf("wechat userinfo decode: %w", err)
	}
	if u.ErrCode != 0 {
		return nil, fmt.Errorf("wechat userinfo: errcode=%d errmsg=%s", u.ErrCode, u.ErrMsg)
	}

	subject := tok.UnionID
	if subject == "" {
		subject = u.UnionID
	}
	if subject == "" {
		subject = tok.OpenID
	}
	if subject == "" {
		return nil, fmt.Errorf("wechat userinfo: missing openid/unionid")
	}
	return &ExternalProfile{
		Provider:    "wechat",
		Subject:     subject,
		DisplayName: u.Nickname,
	}, nil
}
