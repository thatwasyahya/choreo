package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
)

// rehash upgrades a password that was stored with weaker parameters.
//
// Best effort, on purpose: it runs after a SUCCESSFUL sign-in, so the user has
// already proved who they are. A storage hiccup while quietly strengthening
// their hash is not their problem and must not cost them the login.
func (h *handlers) rehash(ctx context.Context, accountID uuid.UUID, password string) {
	hash, err := h.verifier.Hash(ctx, password)
	if err != nil {
		h.logger.WarnContext(ctx, "could not rehash a password",
			slog.String("error", err.Error()))
		return
	}

	if err := h.store.Identity.SetPassword(ctx, accountID, hash); err != nil {
		h.logger.WarnContext(ctx, "could not store a rehashed password",
			slog.String("error", err.Error()))
	}
}

type createAccountRequest struct {
	Login      string `json:"login"`
	Password   string `json:"password"`
	Protocol   int    `json:"protocol"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`
}

type createAccountResponse struct {
	AccountID string `json:"account_id"`
	DeviceID  string `json:"device_id"`
	tokenPair
	// RecoveryCode appears here and nowhere else, ever. The server keeps only a
	// hash and cannot show it again — it is the only safety net when there is
	// no e-mail address, so the interface must force the user to write it down
	// before moving on.
	RecoveryCode string `json:"recovery_code"`
}

func (h *handlers) createAccount(w http.ResponseWriter, r *http.Request) {
	if !allow(w, h.limits.SignUp, clientKey(r)) {
		return
	}

	var req createAccountRequest
	if !decode(w, r, &req) {
		return
	}

	if err := auth.ValidateLogin(req.Login); err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	ctx := r.Context()

	pwHash, err := h.verifier.Hash(ctx, req.Password)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	recoveryCode, recoveryDigest, err := auth.NewRecoveryCode()
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	// The recovery code is a credential, so it is hashed like one — argon2id
	// and not sha256. Unlike a token, it is short enough to be worth attacking.
	recoveryHash, err := h.verifier.Hash(ctx, string(recoveryDigest))
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	accountID, err := h.store.Identity.CreateAccount(ctx, req.Login, pwHash, recoveryHash)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	deviceID, err := h.store.Identity.RegisterDevice(
		ctx, accountID, req.AppVersion, req.Protocol, req.Platform)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	pair, err := h.issue(ctx, accountID, deviceID, nil)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, createAccountResponse{
		AccountID:    accountID.String(),
		DeviceID:     deviceID.String(),
		tokenPair:    pair,
		RecoveryCode: recoveryCode,
	})
}

type changePasswordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// changePassword rotates a password without touching anything else.
//
// The other devices are deliberately NOT revoked: changing a password out of
// hygiene should not sign the kitchen tablet out. Recovery is the operation
// that revokes, because there the premise is that a device is in the wrong
// hands.
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}

	if err := auth.ValidatePassword(req.New); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	ctx := r.Context()
	session := caller(ctx)

	account, err := h.store.Identity.AccountByID(ctx, session.AccountID)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	ok, _, err := h.verifier.Verify(ctx, account.PasswordHash, req.Current)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeBadCredentials})
		return
	}

	hash, err := h.verifier.Hash(ctx, req.New)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	if err := h.store.Identity.SetPassword(ctx, session.AccountID, hash); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
