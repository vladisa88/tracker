package app

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const telegramIssuer = "https://oauth.telegram.org"

func strconvID(id int64) string { return strconv.FormatInt(id, 10) }

type Telegram struct {
	config Config
	Client *http.Client
}

func NewTelegram(c Config) *Telegram {
	return &Telegram{config: c, Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (t *Telegram) AuthorizationURL(state, nonce, challenge string) string {
	q := url.Values{"client_id": {t.config.ClientID}, "redirect_uri": {callbackURL(t.config.Origin)}, "response_type": {"code"}, "scope": {"openid profile"}, "state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return telegramIssuer + "/auth?" + q.Encode()
}
func (t *Telegram) Exchange(ctx context.Context, code, verifier, nonce string, now time.Time) (int64, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callbackURL(t.config.Origin)}, "client_id": {t.config.ClientID}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, "POST", telegramIssuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(t.config.ClientID, t.config.ClientSecret)
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err = t.request(req, &tokens); err != nil {
		return 0, err
	}
	return t.verify(ctx, tokens.IDToken, nonce, now)
}
func (t *Telegram) request(req *http.Request, dest any) error {
	res, err := t.Client.Do(req)
	if err != nil {
		return errors.New("Telegram unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("Telegram rejected request")
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(dest); err != nil {
		return errors.New("invalid Telegram response")
	}
	return nil
}
func (t *Telegram) verify(ctx context.Context, token, nonce string, now time.Time) (int64, error) {
	invalid := errors.New("invalid Telegram identity")
	if len(token) > 65536 {
		return 0, invalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, invalid
	}
	decode := base64.RawURLEncoding.DecodeString
	head, err := decode(parts[0])
	if err != nil {
		return 0, invalid
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(head, &header) != nil || header.Alg != "RS256" || header.Kid == "" {
		return 0, invalid
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", telegramIssuer+"/.well-known/jwks.json", nil)
	var keys struct {
		Keys []struct{ Kty, Kid, N, E, Alg, Use string } `json:"keys"`
	}
	if err := t.request(req, &keys); err != nil {
		return 0, err
	}
	signature, err := decode(parts[2])
	if err != nil {
		return 0, invalid
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	for _, k := range keys.Keys {
		if k.Kid != header.Kid || k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e1 := decode(k.N)
		e, e2 := decode(k.E)
		if e1 != nil || e2 != nil || len(e) > 4 {
			continue
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent*256 + int(b)
		}
		key := rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
		if key.N.BitLen() < 2048 || exponent < 3 {
			continue
		}
		if rsa.VerifyPKCS1v15(&key, crypto.SHA256, digest[:], signature) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return 0, invalid
	}
	body, err := decode(parts[1])
	if err != nil {
		return 0, invalid
	}
	var claims struct {
		Iss   string          `json:"iss"`
		Sub   string          `json:"sub"`
		Aud   json.RawMessage `json:"aud"`
		Exp   int64           `json:"exp"`
		Iat   int64           `json:"iat"`
		Nbf   int64           `json:"nbf"`
		ID    int64           `json:"id"`
		Nonce string          `json:"nonce"`
		AZP   string          `json:"azp"`
	}
	if json.Unmarshal(body, &claims) != nil {
		return 0, invalid
	}
	var audience string
	var audiences []string
	if json.Unmarshal(claims.Aud, &audience) == nil {
		audiences = []string{audience}
	} else if json.Unmarshal(claims.Aud, &audiences) != nil {
		return 0, invalid
	}
	match := false
	for _, a := range audiences {
		if a == t.config.ClientID {
			match = true
		}
	}
	if !match || (len(audiences) > 1 && claims.AZP != t.config.ClientID) || (claims.AZP != "" && claims.AZP != t.config.ClientID) || claims.Iss != telegramIssuer || claims.Sub == "" || claims.ID <= 0 || claims.Exp <= now.Unix() || claims.Iat <= 0 || claims.Iat > now.Unix() || claims.Iat > claims.Exp || claims.Nbf > now.Unix() || !equal(claims.Nonce, nonce) || nonce == "" {
		return 0, invalid
	}
	return claims.ID, nil
}

// Keep errors deliberately independent of provider response bodies and credentials.
