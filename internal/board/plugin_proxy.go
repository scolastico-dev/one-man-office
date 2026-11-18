package board

import (
	"context"
	"database/sql"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func pluginProxyActor(a Actor) error {
	if a.Role != api.RoleSystem {
		return api.Errorf(api.CodeForbidden, "only omo binds plugin forwards")
	}
	return nil
}

// AttachPluginForward records one immutable stable run identity.
func (b *Board) AttachPluginForward(ctx context.Context, a Actor, plugin, id, task string) error {
	if err := pluginProxyActor(a); err != nil {
		return err
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		f, err := loadProxyForward(tx, id)
		if err != nil {
			return err
		}
		if plugin == "" || f.Plugin != plugin {
			return proxyNotFound()
		}
		if task == "" || (f.TaskID != "" && f.TaskID != task) {
			return api.Errorf(api.CodeValidation, "forward task binding is immutable")
		}
		if f.TaskID == task {
			return nil
		}
		if _, err := tx.Exec(`UPDATE proxy_forwards SET task_id = ? WHERE id = ?`, task, id); err != nil {
			return err
		}
		f.TaskID = task
		return emit(tx, api.KindProxyForwardUpdated, proxyTicket(f), a, f)
	})
}

// CreatePluginForward shares normal registry validation and event persistence.
// The server supplies the plugin identity; public request types have no owner.
func (b *Board) CreatePluginForward(ctx context.Context, a Actor, plugin, task string, req api.CreateProxyForward) (api.ProxyForward, error) {
	if err := pluginProxyActor(a); err != nil {
		return api.ProxyForward{}, err
	}
	if plugin == "" {
		return api.ProxyForward{}, api.Errorf(api.CodeValidation, "plugin is required")
	}
	return b.createProxyForward(ctx, a, req, plugin, task)
}

func (b *Board) PluginForwards(ctx context.Context, a Actor, plugin string) ([]api.ProxyForward, error) {
	if err := pluginProxyActor(a); err != nil {
		return nil, err
	}
	out := []api.ProxyForward{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		return each(tx, proxySelect+` WHERE f.plugin_owner = ? ORDER BY f.id`, plugin, func(rows *sql.Rows) error {
			f, e := scanProxyForward(rows)
			if e == nil {
				out = append(out, f)
			}
			return e
		})
	})
	return out, err
}

func (b *Board) DeletePluginForward(ctx context.Context, a Actor, plugin, id string) error {
	if err := pluginProxyActor(a); err != nil {
		return err
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		f, e := loadProxyForward(tx, id)
		if e != nil {
			return e
		}
		if plugin == "" || f.Plugin != plugin {
			return proxyNotFound()
		}
		return removeProxyForward(tx, a, f)
	})
}

// ClearPluginForwards removes one task, one namespace, or all stale plugin
// resources at startup. Generic user forwards always survive this operation.
func (b *Board) ClearPluginForwards(ctx context.Context, a Actor, plugin, task string) error {
	if err := pluginProxyActor(a); err != nil {
		return err
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		query := proxySelect + ` WHERE f.plugin_owner <> ''`
		args := []any{}
		if plugin != "" {
			query += ` AND f.plugin_owner = ?`
			args = append(args, plugin)
		}
		if task != "" {
			query += ` AND f.task_id = ?`
			args = append(args, task)
		}
		// Finish reading before deleting, on the transaction's single connection.
		rows, e := tx.Query(query, args...)
		if e != nil {
			return e
		}
		var forwards []api.ProxyForward
		for rows.Next() {
			f, e := scanProxyForward(rows)
			if e != nil {
				rows.Close()
				return e
			}
			forwards = append(forwards, f)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, f := range forwards {
			if e := removeProxyForward(tx, a, f); e != nil {
				return e
			}
		}
		return nil
	})
}
