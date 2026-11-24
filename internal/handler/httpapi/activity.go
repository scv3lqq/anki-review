package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

type activityCollection interface {
	Activity(context.Context, time.Time) ([]domain.ActivityDay, error)
}

func (h *handler) activity(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("timezone")
	loc := time.UTC
	if zone != "" {
		var err error
		loc, err = time.LoadLocation(zone)
		if err != nil || zone == "Local" {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "Invalid timezone"})
			return
		}
	}
	c, ok := h.opts.Collection.(activityCollection)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: collectionUnavailable})
		return
	}
	days, err := c.Activity(r.Context(), time.Now().In(loc))
	if err != nil {
		h.collectionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}
