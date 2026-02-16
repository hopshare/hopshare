package templates

import "context"

type adminContextKey struct{}

func WithAdmin(ctx context.Context, isAdmin bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, adminContextKey{}, isAdmin)
}

func IsAdmin(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	isAdmin, _ := ctx.Value(adminContextKey{}).(bool)
	return isAdmin
}
