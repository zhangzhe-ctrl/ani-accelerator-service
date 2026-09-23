package biz

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

type Caller struct {
	Service    string
	Actor      Actor
	TenantID   string
	Action     string
	ClusterIDs []string
}
type callerKey struct{}

func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}
func CurrentCaller(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
func AuthorizeAdmin(ctx context.Context, a *AdminRead, method string) error {
	if a == nil || !Ref(a.RequestID) {
		return Invalid("INVALID_ADMIN_CONTEXT")
	}
	if err := ValidateActor(a.Actor); err != nil {
		return err
	}
	c, ok := CurrentCaller(ctx)
	if !ok || c.Service != "ani-governance" || c.TenantID != "" || c.Actor != *a.Actor || c.Action != method {
		return Denied()
	}
	return nil
}
func AuthorizeTenant(ctx context.Context, a *TenantContext, method string) error {
	if a == nil || !UUID(a.TenantID) || !Ref(a.RequestID) {
		return Invalid("INVALID_TENANT_CONTEXT")
	}
	if err := ValidateActor(a.Actor); err != nil {
		return err
	}
	c, ok := CurrentCaller(ctx)
	if !ok || c.Service != "ani-governance" || c.TenantID != a.TenantID || c.Actor != *a.Actor || c.Action != method {
		return Denied()
	}
	return nil
}
func AuthorizeCluster(ctx context.Context, cluster string) error {
	c, ok := CurrentCaller(ctx)
	if !ok {
		return Denied()
	}
	for _, id := range c.ClusterIDs {
		if id == cluster || (id == "*" && c.TenantID == "") {
			return nil
		}
	}
	return NotFound()
}
func AuthorizeMutation(ctx context.Context, m *AdminMutation, method string) error {
	if m == nil || !UUID(m.IdempotencyKey) {
		return Invalid("INVALID_IDEMPOTENCY_KEY")
	}
	return AuthorizeAdmin(ctx, m.Context, method)
}

type CursorCodec struct{ key []byte }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) < 32 {
		return nil, Invalid("CURSOR_KEY_REQUIRED")
	}
	return &CursorCodec{append([]byte{}, key...)}, nil
}

type pageCursor struct {
	Scope string `json:"scope"`
	After string `json:"after"`
}

func (c *CursorCodec) Decode(p *Page, scope string) (string, int, error) {
	if p == nil {
		return "", 50, nil
	}
	size := int(p.Size)
	if size == 0 {
		size = 50
	}
	if size > 200 || len(p.Token) > 2048 {
		return "", 0, Invalid("INVALID_PAGE")
	}
	if p.Token == "" {
		return "", size, nil
	}
	parts := strings.Split(p.Token, ".")
	if len(parts) != 2 {
		return "", 0, Invalid("INVALID_PAGE")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return "", 0, Invalid("INVALID_PAGE")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", 0, Invalid("INVALID_PAGE")
	}
	m := hmac.New(sha256.New, c.key)
	m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return "", 0, Invalid("INVALID_PAGE")
	}
	var v pageCursor
	if json.Unmarshal(b, &v) != nil || v.Scope != scope || !UUID(v.After) {
		return "", 0, Invalid("PAGE_SCOPE_MISMATCH")
	}
	return v.After, size, nil
}
func (c *CursorCodec) Encode(scope, after string) string {
	b, _ := json.Marshal(pageCursor{scope, after})
	m := hmac.New(sha256.New, c.key)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func PageScope(ctx context.Context, method string, filter any) (string, error) {
	c, ok := CurrentCaller(ctx)
	if !ok {
		return "", Denied()
	}
	return Hash(struct {
		Caller Caller
		Method string
		Filter any
	}{c, method, filter})
}
