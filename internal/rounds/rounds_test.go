package rounds

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnamadeo/mealbot/internal/db"
)

func newTestHandler(t *testing.T) (*Handler, sqlmock.Sqlmock) {
	t.Helper()
	conn, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return NewHandler(conn), mock
}

// Successful responses are written with the implicit 200 status (LogAndWrite only logs the status).
func TestHandlerMethodAndParamValidation(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		call       func(h *Handler) http.HandlerFunc
		wantStatus int
	}{
		{"add wrong method", http.MethodGet, "/rounds?org=o&round=d", func(h *Handler) http.HandlerFunc { return h.AddRoundHandler }, http.StatusMethodNotAllowed},
		{"add missing org", http.MethodPost, "/rounds?round=d", func(h *Handler) http.HandlerFunc { return h.AddRoundHandler }, http.StatusBadRequest},
		{"add missing round", http.MethodPost, "/rounds?org=o", func(h *Handler) http.HandlerFunc { return h.AddRoundHandler }, http.StatusBadRequest},
		{"add duplicate param", http.MethodPost, "/rounds?org=o&org=p&round=d", func(h *Handler) http.HandlerFunc { return h.AddRoundHandler }, http.StatusBadRequest},
		{"get wrong method", http.MethodPost, "/rounds?org=o", func(h *Handler) http.HandlerFunc { return h.GetRoundsHandler }, http.StatusMethodNotAllowed},
		{"get missing org", http.MethodGet, "/rounds", func(h *Handler) http.HandlerFunc { return h.GetRoundsHandler }, http.StatusBadRequest},
		{"remove wrong method", http.MethodGet, "/rounds?org=o&roundId=1", func(h *Handler) http.HandlerFunc { return h.RemoveRoundHandler }, http.StatusMethodNotAllowed},
		{"remove missing roundId", http.MethodDelete, "/rounds?org=o", func(h *Handler) http.HandlerFunc { return h.RemoveRoundHandler }, http.StatusBadRequest},
		{"remove non-numeric roundId", http.MethodDelete, "/rounds?org=o&roundId=abc", func(h *Handler) http.HandlerFunc { return h.RemoveRoundHandler }, http.StatusInternalServerError},
		{"reschedule wrong method", http.MethodGet, "/rounds?org=o&round=d&roundId=1", func(h *Handler) http.HandlerFunc { return h.RescheduleRoundHandler }, http.StatusMethodNotAllowed},
		{"reschedule missing round", http.MethodPost, "/rounds?org=o&roundId=1", func(h *Handler) http.HandlerFunc { return h.RescheduleRoundHandler }, http.StatusBadRequest},
		{"reschedule non-numeric roundId", http.MethodPost, "/rounds?org=o&round=d&roundId=x", func(h *Handler) http.HandlerFunc { return h.RescheduleRoundHandler }, http.StatusInternalServerError},
		{"combined wrong method", http.MethodGet, "/rounds?org=o", func(h *Handler) http.HandlerFunc { return h.RoundHandler }, http.StatusMethodNotAllowed},
		{"combined put", http.MethodPut, "/rounds?org=o", func(h *Handler) http.HandlerFunc { return h.RoundHandler }, http.StatusMethodNotAllowed},
		{"combined post missing params", http.MethodPost, "/rounds", func(h *Handler) http.HandlerFunc { return h.RoundHandler }, http.StatusBadRequest},
		{"combined delete missing params", http.MethodDelete, "/rounds", func(h *Handler) http.HandlerFunc { return h.RoundHandler }, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock := newTestHandler(t)
			rec := httptest.NewRecorder()
			tt.call(h)(rec, httptest.NewRequest(tt.method, tt.target, nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			// validation failures must not touch the database
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAddRoundHandler(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(m sqlmock.Sqlmock)
		wantStatus int
		wantBody   string
	}{
		{
			name: "first round gets id 0",
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(`SELECT MAX\(id\) FROM rounds`).WithArgs("org").
					WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
				m.ExpectExec(`INSERT INTO rounds`).WithArgs("org", 0, "2024-01-01", false).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantStatus: http.StatusOK,
			wantBody:   "Successfully scheduled",
		},
		{
			name: "next id follows max",
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(`SELECT MAX\(id\) FROM rounds`).WithArgs("org").
					WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(int64(4)))
				m.ExpectExec(`INSERT INTO rounds`).WithArgs("org", 5, "2024-01-01", false).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "duplicate key",
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(`SELECT MAX\(id\) FROM rounds`).WithArgs("org").
					WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(int64(1)))
				m.ExpectExec(`INSERT INTO rounds`).
					WillReturnError(errors.New(db.DuplicateKeyErr))
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   db.DuplicateKeyErr,
		},
		{
			name: "max query fails",
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectQuery(`SELECT MAX\(id\) FROM rounds`).WillReturnError(errors.New("boom"))
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock := newTestHandler(t)
			tt.setup(mock)
			rec := httptest.NewRecorder()
			h.AddRoundHandler(rec, httptest.NewRequest(http.MethodPost, "/rounds?org=org&round=2024-01-01", nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAddRoundDuplicateKeyErrReturned(t *testing.T) {
	conn, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer conn.Close()

	mock.ExpectQuery(`SELECT MAX\(id\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectExec(`INSERT INTO rounds`).WillReturnError(errors.New(db.DuplicateKeyErr))

	err = AddRound(conn, "org", "2024-01-01")
	require.Error(t, err)
	assert.Contains(t, err.Error(), db.DuplicateKeyErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRoundsHandler(t *testing.T) {
	t.Run("returns dates in order", func(t *testing.T) {
		h, mock := newTestHandler(t)
		mock.ExpectQuery(`SELECT to_char\(scheduled_date, \$1\) FROM rounds WHERE organization = \$2 ORDER BY id`).
			WithArgs(TimestampFormat, "org").
			WillReturnRows(sqlmock.NewRows([]string{"d"}).AddRow("2024-01-01 00:00:00Z").AddRow("2024-02-01 00:00:00Z"))

		rec := httptest.NewRecorder()
		h.GetRoundsHandler(rec, httptest.NewRequest(http.MethodGet, "/rounds?org=org", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"rounds":["2024-01-01 00:00:00Z","2024-02-01 00:00:00Z"]}`, rec.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rounds gives empty list", func(t *testing.T) {
		h, mock := newTestHandler(t)
		mock.ExpectQuery(`FROM rounds`).WillReturnRows(sqlmock.NewRows([]string{"d"}))

		rec := httptest.NewRecorder()
		h.GetRoundsHandler(rec, httptest.NewRequest(http.MethodGet, "/rounds?org=org", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"rounds":[]}`, rec.Body.String())
	})

	t.Run("db error", func(t *testing.T) {
		h, mock := newTestHandler(t)
		mock.ExpectQuery(`FROM rounds`).WillReturnError(errors.New("boom"))

		rec := httptest.NewRecorder()
		h.GetRoundsHandler(rec, httptest.NewRequest(http.MethodGet, "/rounds?org=org", nil))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestRemoveRound(t *testing.T) {
	tests := []struct {
		name    string
		roundID int
		setup   func(m sqlmock.Sqlmock)
		wantErr string
	}{
		{
			name:    "shifts later rounds down until none left",
			roundID: 2,
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectExec(`DELETE FROM rounds WHERE organization = \$1 AND id = \$2`).
					WithArgs("org", 2).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(`UPDATE rounds SET id = \$1 WHERE organization = \$2 AND id = \$3`).
					WithArgs(2, "org", 3).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(`UPDATE rounds SET id`).
					WithArgs(3, "org", 4).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(`UPDATE rounds SET id`).
					WithArgs(4, "org", 5).WillReturnResult(sqlmock.NewResult(0, 0))
			},
		},
		{
			name:    "last round needs no shifting past one update",
			roundID: 0,
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectExec(`DELETE FROM rounds`).WithArgs("org", 0).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(`UPDATE rounds SET id`).WithArgs(0, "org", 1).WillReturnResult(sqlmock.NewResult(0, 0))
			},
		},
		{
			name:    "delete error",
			roundID: 1,
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectExec(`DELETE FROM rounds`).WillReturnError(errors.New("delete failed"))
			},
			wantErr: "delete failed",
		},
		{
			name:    "update error",
			roundID: 1,
			setup: func(m sqlmock.Sqlmock) {
				m.ExpectExec(`DELETE FROM rounds`).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec(`UPDATE rounds SET id`).WillReturnError(errors.New("update failed"))
			},
			wantErr: "update failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer conn.Close()
			tt.setup(mock)

			err = RemoveRound(conn, "org", tt.roundID)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRemoveRoundHandler(t *testing.T) {
	h, mock := newTestHandler(t)
	mock.ExpectExec(`DELETE FROM rounds`).WithArgs("org", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE rounds SET id`).WithArgs(1, "org", 2).WillReturnResult(sqlmock.NewResult(0, 0))

	rec := httptest.NewRecorder()
	h.RoundHandler(rec, httptest.NewRequest(http.MethodDelete, "/rounds?org=org&roundId=1", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Successfully removed round")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRescheduleRound(t *testing.T) {
	tests := []struct {
		name    string
		execErr error
	}{
		{"success", nil},
		{"db error", errors.New("boom")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer conn.Close()

			exp := mock.ExpectExec(`UPDATE rounds SET scheduled_date = \$1 WHERE organization = \$2 AND id = \$3`).
				WithArgs("2024-03-01", "org", 4)
			if tt.execErr != nil {
				exp.WillReturnError(tt.execErr)
			} else {
				exp.WillReturnResult(sqlmock.NewResult(0, 1))
			}

			err = RescheduleRound(conn, "org", "2024-03-01", 4)
			if tt.execErr != nil {
				assert.EqualError(t, err, tt.execErr.Error())
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRoundHandlerDispatchesReschedule(t *testing.T) {
	h, mock := newTestHandler(t)
	mock.ExpectExec(`UPDATE rounds SET scheduled_date`).
		WithArgs("2024-03-01", "org", 4).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.RoundHandler(rec, httptest.NewRequest(http.MethodPost, "/rounds?org=org&round=2024-03-01&roundId=4", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "changed date")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRoundHandlerDispatchesAdd(t *testing.T) {
	h, mock := newTestHandler(t)
	mock.ExpectQuery(`SELECT MAX\(id\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectExec(`INSERT INTO rounds`).WithArgs("org", 0, "2024-03-01", false).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.RoundHandler(rec, httptest.NewRequest(http.MethodPost, "/rounds?org=org&round=2024-03-01", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
