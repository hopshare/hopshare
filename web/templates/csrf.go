package templates

import "context"

const CSRFFieldName = "csrf_token"

type csrfTokenContextKey struct{}

func WithCSRFToken(ctx context.Context, token string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, csrfTokenContextKey{}, token)
}

func CSRFToken(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	token, _ := ctx.Value(csrfTokenContextKey{}).(string)
	return token
}
