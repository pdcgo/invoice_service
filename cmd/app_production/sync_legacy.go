package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"github.com/pdcgo/schema/services/invoice_iface/v2/invoice_ifaceconnect"
	role_base "github.com/pdcgo/schema/services/role_base/v1"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/user_service/identity"
	"github.com/pdcgo/user_service/user_models"
	"github.com/urfave/cli/v3"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

type SyncLegacyFunc cli.ActionFunc

// NewSyncLegacyFunc builds the `sync-legacy` action: it lists every team and asks
// the running invoice service (at --host) to reconcile each team's balances to
// legacy truth via the TeamReconcile RPC.
//
// TeamReconcile requires a root/admin caller. The operator passes --username of
// such a user; the action mints a short-lived JWT for them (signed with the
// service's JwtSecret) and attaches it to every request. TeamReconcile is diff-
// based (idempotent), so re-running is safe, and a per-team failure is logged and
// skipped rather than aborting the whole run.
func NewSyncLegacyFunc(db *gorm.DB, cfg *configs.AppConfig) SyncLegacyFunc {
	return func(ctx context.Context, c *cli.Command) error {
		host := c.String("host")
		username := c.String("username")

		token, err := mintToken(ctx, db, cfg.JwtSecret, username)
		if err != nil {
			return err
		}

		client := invoice_ifaceconnect.NewInvoiceServiceClient(
			http.DefaultClient, host,
			connect.WithInterceptors(&bearerAuth{token: token}),
		)

		var teamIDs []uint
		err = db.WithContext(ctx).
			Model(&db_models.Team{}).
			Where("deleted = ?", false).
			Pluck("id", &teamIDs).
			Error
		if err != nil {
			return err
		}

		log.Printf("sync-legacy: %d team(s) as %q via %s", len(teamIDs), username, host)

		var failed int
		for _, teamID := range teamIDs {
			_, err := client.TeamReconcile(ctx, connect.NewRequest(&invoice_iface.TeamReconcileRequest{
				TeamId: uint64(teamID),
			}))
			if err != nil {
				log.Printf("sync-legacy: team %d FAILED: %v", teamID, err)
				failed++
				continue
			}
			log.Printf("sync-legacy: team %d reconciled", teamID)
		}

		if failed > 0 {
			return fmt.Errorf("sync-legacy: %d/%d team(s) failed", failed, len(teamIDs))
		}
		log.Printf("sync-legacy: done, %d team(s) reconciled", len(teamIDs))
		return nil
	}
}

// mintToken looks up the named user and signs a short-lived system JWT for them
// using the shared secret, so the CLI can authenticate as a root/admin caller.
func mintToken(ctx context.Context, db *gorm.DB, secret, username string) (string, error) {
	if username == "" {
		return "", fmt.Errorf("--username is required (a root/admin user to authenticate as)")
	}

	var usr user_models.User
	err := db.WithContext(ctx).Where("username = ?", username).First(&usr).Error
	if err != nil {
		return "", fmt.Errorf("lookup user %q: %w", username, err)
	}

	tok := &identity.TokenIdentity{
		Identity: &role_base.Identity{
			IdentityId:   uint32(usr.ID),
			IdentityType: role_base.IdentityType_IDENTITY_TYPE_SYSTEM,
			Username:     usr.Username,
			ExpiredAt:    timestamppb.New(time.Now().Add(time.Hour)),
		},
	}
	return tok.Serialize(secret)
}

// bearerAuth attaches an Authorization: Bearer <token> header to outgoing client
// requests (unary and streaming).
type bearerAuth struct{ token string }

func (b *bearerAuth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			req.Header().Set("Authorization", "Bearer "+b.token)
		}
		return next(ctx, req)
	}
}

func (b *bearerAuth) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set("Authorization", "Bearer "+b.token)
		return conn
	}
}

func (b *bearerAuth) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
