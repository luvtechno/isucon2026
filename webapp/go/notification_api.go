package isureal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getNotificationsResponse struct {
	ID              string    `json:"id" db:"id"`
	PostingWindowID string    `json:"postingWindowId" db:"posting_window_id"`
	Region          string    `json:"region" db:"region"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
}

type openedWindow struct {
	ID       string    `db:"id"`
	Region   string    `db:"region"`
	StartsAt time.Time `db:"starts_at"`
}

// windowWatcher は投稿期間の開始を1本のループで見て、SSE の接続へ配る。
type windowWatcher struct {
	mu      sync.Mutex
	region  string
	current openedWindow
	waiters map[chan openedWindow]struct{}
}

const windowWatchInterval = 50 * time.Millisecond

func newWindowWatcher(db *sqlx.DB, region string) *windowWatcher {
	w := &windowWatcher{region: region, waiters: map[chan openedWindow]struct{}{}}
	go w.run(db)
	return w
}

func (w *windowWatcher) run(db *sqlx.DB) {
	for {
		time.Sleep(windowWatchInterval)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var opened openedWindow
		err := db.GetContext(ctx, &opened, `SELECT id, region, starts_at FROM posting_windows WHERE region = $1 AND ended_at IS NULL`, w.region)
		cancel()
		if err != nil {
			continue
		}
		w.mu.Lock()
		if opened.ID != w.current.ID {
			w.current = opened
			for ch := range w.waiters {
				select {
				case ch <- opened:
				default:
				}
			}
		}
		w.mu.Unlock()
	}
}

func (w *windowWatcher) subscribe() chan openedWindow {
	ch := make(chan openedWindow, 1)
	w.mu.Lock()
	w.waiters[ch] = struct{}{}
	w.mu.Unlock()
	return ch
}

func (w *windowWatcher) unsubscribe(ch chan openedWindow) {
	w.mu.Lock()
	delete(w.waiters, ch)
	w.mu.Unlock()
}

func getNotificationsHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := c.Request().Context()
		session := GetSession(c)
		userID := session.UserID
		connectedAt := time.Now()
		ch := h.Windows.subscribe()
		defer h.Windows.unsubscribe(ch)
		response := c.Response()
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		controller := http.NewResponseController(response)
		if err := controller.Flush(); err != nil {
			return nil
		}
		// 宛先の退会やリージョンの変更を見るのは、たまにでよい
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				user, err := getUser(ctx, h.DB, userID)
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
						c.Logger().Error("failed to get notification recipient", "error", err)
					}
					return nil
				}
				if user.Region != session.Region {
					return nil
				}
			case opened := <-ch:
				if opened.StartsAt.Before(connectedAt.Add(-time.Second)) {
					continue
				}
				data, err := json.Marshal(getNotificationsResponse{ID: uuid.NewString(), PostingWindowID: opened.ID, Region: opened.Region, CreatedAt: opened.StartsAt})
				if err != nil {
					c.Logger().Error("failed to encode notification", "error", err)
					return nil
				}
				if _, err := fmt.Fprintf(response, "data: %s\n\n", data); err != nil {
					return nil
				}
				_ = controller.Flush()
				return nil
			}
		}
	}
}
