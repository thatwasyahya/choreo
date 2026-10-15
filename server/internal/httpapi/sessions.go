package httpapi

import (
	"errors"
	"net/http"

	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

type signInRequest struct {
	Login      string `json:"login"`
	Password   string `json:"password"`
	Protocol   int    `json:"protocol"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`
	// DeviceID is optional. Presenting the one this installation already has is
	// what lets it sign back in without losing its cursor.
	DeviceID string `json:"device_id"`
}

type signInResponse struct {
	AccountID string `json:"account_id"`
	DeviceID  string `json:"device_id"`
	tokenPair
}

func (h *handlers) signIn(w http.ResponseWriter, r *http.Request) {
	var req signInRequest
	if !decode(w, r, &req) {
		return
	}

	// Two buckets: one per login, so one account cannot be hammered, and one
	// per address, so one address cannot hammer every account.
	if !allow(w, h.limits.SignInIP, clientKey(r)) {
		return
	}
	if !allow(w, h.limits.SignIn, req.Login) {
		return
	}

	ctx := r.Context()

	account, err := h.store.Identity.AccountByLogin(ctx, req.Login)
	if err != nil {
		// Spend the hashing budget anyway. Answering instantly for an unknown
		// login and slowly for a real one tells an attacker which logins exist,
		// and the rate limit does not hide a timing difference.
		h.verifier.SpendBudget(ctx)
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeBadCredentials})
		return
	}

	ok, needsRehash, err := h.verifier.Verify(ctx, account.PasswordHash, req.Password)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeBadCredentials})
		return
	}

	if needsRehash {
		h.rehash(ctx, account.ID, req.Password)
	}

	deviceID, err := h.resolveDevice(
		ctx, account.ID, req.DeviceID, req.AppVersion, req.Protocol, req.Platform)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	pair, err := h.issue(ctx, account.ID, deviceID, nil)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, signInResponse{
		AccountID: account.ID.String(),
		DeviceID:  deviceID.String(),
		tokenPair: pair,
	})
}

type refreshRequest struct {
	Refresh string `json:"refresh"`
}

func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Refresh == "" {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeUnauthenticated})
		return
	}

	ctx := r.Context()

	previous, err := h.store.Identity.RotateRefresh(ctx, auth.TokenDigest(req.Refresh))
	if err != nil {
		if errors.Is(err, store.ErrRefreshReused) {
			// Two parties are alternating on one chain and we cannot tell which
			// is the owner. Drop this device's sessions so both have to
			// authenticate again — but do not revoke the device itself: the
			// legitimate user keeps their local replica and simply signs in.
			if dropErr := h.store.Identity.RevokeDeviceSessions(ctx, previous.DeviceID); dropErr != nil {
				writeError(w, r, h.logger, dropErr)
				return
			}
		}
		writeError(w, r, h.logger, err)
		return
	}

	pair, err := h.issue(ctx, previous.AccountID, previous.DeviceID, &previous.ID)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, pair)
}

// signOut drops this installation's session and nothing else.
//
// The local replica stays: signing out is not being wiped. Only "remove this
// device" does that, and it is a different endpoint on purpose.
func (h *handlers) signOut(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Identity.DeleteSession(r.Context(), caller(r.Context()).ID); err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type recoverRequest struct {
	Login        string `json:"login"`
	RecoveryCode string `json:"recovery_code"`
	NewPassword  string `json:"new_password"`
	Protocol     int    `json:"protocol"`
	AppVersion   string `json:"app_version"`
	Platform     string `json:"platform"`
}

type recoverResponse struct {
	AccountID string `json:"account_id"`
	DeviceID  string `json:"device_id"`
	tokenPair
	RecoveryCode string `json:"recovery_code"`
}

// recoverAccount is the only way back in without a password.
//
// It revokes every other device. The premise of recovery is "I lost my phone
// and forgot my password", and leaving the lost handset signed in is the thing
// nobody wants. The asymmetry decides it: revoking wrongly costs one sign-in,
// not revoking wrongly lets whoever found the recovery code live alongside the
// owner in silence (decision D-021, A2).
func (h *handlers) recoverAccount(w http.ResponseWriter, r *http.Request) {
	var req recoverRequest
	if !decode(w, r, &req) {
		return
	}

	if !allow(w, h.limits.Recover, req.Login) {
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	ctx := r.Context()

	account, err := h.store.Identity.AccountByLogin(ctx, req.Login)
	if err != nil {
		h.verifier.SpendBudget(ctx)
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeBadCredentials})
		return
	}

	presented := auth.CodeDigest(auth.NormaliseCode(req.RecoveryCode))

	ok, _, err := h.verifier.Verify(ctx, account.RecoveryHash, string(presented))
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: codeBadCredentials})
		return
	}

	pwHash, err := h.verifier.Hash(ctx, req.NewPassword)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	nextCode, nextDigest, err := auth.NewRecoveryCode()
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}
	nextHash, err := h.verifier.Hash(ctx, string(nextDigest))
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	// Both at once: the old recovery code is spent by definition.
	if err := h.store.Identity.SetPasswordAndRecovery(
		ctx, account.ID, pwHash, nextHash); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	deviceID, err := h.store.Identity.RegisterDevice(
		ctx, account.ID, req.AppVersion, req.Protocol, req.Platform)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	if err := h.store.Identity.RevokeOtherDevices(ctx, account.ID, deviceID); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	pair, err := h.issue(ctx, account.ID, deviceID, nil)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, recoverResponse{
		AccountID:    account.ID.String(),
		DeviceID:     deviceID.String(),
		tokenPair:    pair,
		RecoveryCode: nextCode,
	})
}
