package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

// TokenOut is an API token's metadata. The secret is never included.
type TokenOut struct {
	ID         int64      `json:"id" doc:"Token ID."`
	Name       string     `json:"name" doc:"Label chosen by the owner."`
	Prefix     string     `json:"prefix" doc:"First 12 characters of the token, to recognise it."`
	Scopes     []string   `json:"scopes" doc:"Scopes the token grants: read, control, admin."`
	CreatedAt  time.Time  `json:"created_at" doc:"Creation time, UTC."`
	LastUsedAt *time.Time `json:"last_used_at" nullable:"true" doc:"Last time the token authenticated a request, UTC; null if never. Updated at most once a minute."`
}

// CreatedToken is TokenOut plus the plaintext secret, returned once.
type CreatedToken struct {
	Token string `json:"token" doc:"The API token. Shown only in this response; store it now."`
	TokenOut
}

func tokenOut(t store.APIToken) TokenOut {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return TokenOut{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: scopes,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

type listTokensIn struct{}

type listTokensOut struct {
	Body []TokenOut
}

type createTokenIn struct {
	Body struct {
		Name   string   `json:"name" minLength:"1" maxLength:"100" doc:"Label for the token, 1 to 100 characters."`
		Scopes []string `json:"scopes" doc:"Scopes to grant, a non-empty subset of read, control, admin that the signed-in user holds."`
	}
}

type createTokenOut struct {
	Body CreatedToken
}

type deleteTokenIn struct {
	ID int64 `path:"id" doc:"Token ID."`
}

type noContent struct{}

func (d Deps) registerTokens(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/tokens", ID: "listTokens", Tag: "tokens",
		Summary:     "List own API tokens",
		Description: "Returns the caller's own API tokens, without their secrets.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, _ *listTokensIn) (*listTokensOut, error) {
			id, _ := auth.FromContext(ctx)
			ts, err := d.Store.ListAPITokens(ctx, id.User.ID)
			if err != nil {
				return nil, err
			}
			out := &listTokensOut{Body: make([]TokenOut, 0, len(ts))}
			for _, t := range ts {
				out.Body = append(out.Body, tokenOut(t))
			}
			return out, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/tokens", ID: "createToken", Tag: "tokens",
		Summary: "Create an API token",
		Description: "Creates a token for the signed-in user and returns its plaintext exactly once. Session only: " +
			"an API token gets 403 `session_required`. The scopes must be held by the caller, otherwise 403 `scope_exceeds`.",
		Scope: auth.ScopeRead, Status: http.StatusCreated, Errors: []int{http.StatusBadRequest, http.StatusForbidden}},
		func(ctx context.Context, in *createTokenIn) (*createTokenOut, error) {
			id, _ := auth.FromContext(ctx)
			if id.Via != "session" {
				return nil, errSessionRequired()
			}
			scopes, err := auth.ParseScopes(in.Body.Scopes)
			if err != nil {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", err.Error())
			}
			names := make([]string, len(scopes))
			for i, s := range scopes {
				if !id.Has(s) {
					return nil, apiErr(http.StatusForbidden, "scope_exceeds", "token scope "+string(s)+" exceeds your own scopes")
				}
				names[i] = string(s)
			}
			plain, prefix, hash := auth.GenerateToken()
			t, err := d.Store.CreateAPIToken(ctx, id.User.ID, in.Body.Name, prefix, hash, names)
			if err != nil {
				return nil, err
			}
			return &createTokenOut{Body: CreatedToken{Token: plain, TokenOut: tokenOut(t)}}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/tokens/{id}", ID: "deleteToken", Tag: "tokens",
		Summary:     "Revoke an API token",
		Description: "Deletes one of the caller's own tokens. Revocation takes effect immediately. A token owned by someone else returns 404.",
		Scope:       auth.ScopeRead, Status: http.StatusNoContent},
		func(ctx context.Context, in *deleteTokenIn) (*noContent, error) {
			id, _ := auth.FromContext(ctx)
			err := d.Store.DeleteAPIToken(ctx, id.User.ID, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, apiErr(http.StatusNotFound, "not_found", "no such token")
			}
			if err != nil {
				return nil, err
			}
			return &noContent{}, nil
		})
}
