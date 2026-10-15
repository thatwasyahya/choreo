package httpapi

import (
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// defaultInviteTTL matches docs/AUTH.md: long enough to send a code by
// whatever means, short enough that a leaked one stops mattering.
const defaultInviteTTL = 48 * time.Hour

type membershipView struct {
	HouseholdID string `json:"household_id"`
	MemberID    string `json:"member_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	TZ          string `json:"tz"`
}

type meResponse struct {
	AccountID  string           `json:"account_id"`
	Login      string           `json:"login"`
	DeviceID   string           `json:"device_id"`
	Households []membershipView `json:"households"`
}

// me is what the client calls at startup to learn WHAT to synchronise.
//
// It lists only live memberships, so someone removed from a household sees it
// disappear on their next call rather than when their token expires.
func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session := caller(ctx)

	account, err := h.store.Identity.AccountByID(ctx, session.AccountID)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	memberships, err := h.store.Identity.Memberships(ctx, session.AccountID)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	view := make([]membershipView, 0, len(memberships))
	for _, m := range memberships {
		view = append(view, membershipView{
			HouseholdID: m.HouseholdID.String(),
			MemberID:    m.MemberID.String(),
			Name:        m.Name,
			Role:        m.Role,
			TZ:          m.TZ,
		})
	}

	writeJSON(w, http.StatusOK, meResponse{
		AccountID:  account.ID.String(),
		Login:      account.Login,
		DeviceID:   session.DeviceID.String(),
		Households: view,
	})
}

type createHouseholdRequest struct {
	Name        string `json:"name"`
	TZ          string `json:"tz"`
	DisplayName string `json:"display_name"`
}

type createHouseholdResponse struct {
	HouseholdID string `json:"household_id"`
	MemberID    string `json:"member_id"`
	Role        string `json:"role"`
}

func (h *handlers) createHousehold(w http.ResponseWriter, r *http.Request) {
	var req createHouseholdRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{Error: codeMalformed})
		return
	}
	if req.TZ == "" {
		req.TZ = "Europe/Paris"
	}

	ctx := r.Context()

	// The id is minted here rather than in the store, so every store method
	// keeps the same shape and the tenant is always an argument.
	householdID := uuid.New()

	member, err := h.store.CreateHousehold(ctx, householdID, store.NewHousehold{
		Name:        req.Name,
		TZ:          req.TZ,
		AccountID:   caller(ctx).AccountID,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, createHouseholdResponse{
		HouseholdID: householdID.String(),
		MemberID:    member.ID.String(),
		Role:        member.Role,
	})
}

type createInviteRequest struct {
	Role     string  `json:"role"`
	MemberID *string `json:"member_id"`
	TTLHours int     `json:"ttl_hours"`
}

type createInviteResponse struct {
	Code      string    `json:"code"`
	QR        string    `json:"qr"`
	ExpiresAt time.Time `json:"expires_at"`
}

// createInvite mints a code. Admins only.
//
// 403 here and not 404, unlike everywhere else: the caller already knows they
// are a member of this household, so there is nothing left to leak. The 404
// rule exists to hide membership, not permissions.
func (h *handlers) createInvite(
	w http.ResponseWriter, r *http.Request, householdID uuid.UUID, member store.Member,
) {
	if member.Role != store.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorBody{Error: codeForbiddenRole})
		return
	}

	var req createInviteRequest
	if !decode(w, r, &req) {
		return
	}

	// An invite can never grant admin. The database refuses it too, and the
	// double guard is deliberate: a leaked code must not be able to hand over
	// the household.
	switch req.Role {
	case "adult", "child", "guest":
	default:
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{Error: codeForbiddenRole})
		return
	}

	var claim *uuid.UUID
	if req.MemberID != nil {
		parsed, err := uuid.Parse(*req.MemberID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, errorBody{Error: codeNotFound})
			return
		}
		claim = &parsed
	}

	ttl := time.Duration(req.TTLHours) * time.Hour
	if req.TTLHours <= 0 || ttl > defaultInviteTTL {
		ttl = defaultInviteTTL
	}

	code, digest, err := auth.NewInviteCode()
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	expires := time.Now().Add(ttl)

	if _, err := h.store.CreateInvite(r.Context(), householdID, store.NewInvite{
		CodeHash:    digest,
		Role:        req.Role,
		ClaimMember: claim,
		CreatedBy:   member.ID,
		ExpiresAt:   expires,
	}); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, createInviteResponse{
		Code:      code,
		QR:        h.joinURL(code),
		ExpiresAt: expires,
	})
}

// joinURL builds the payload behind the QR code.
//
// It carries the SERVER ADDRESS as well as the code. Without it a household
// running its own instance could not invite anyone: the guest would scan a
// valid code and present it to the wrong server (principle 8).
func (h *handlers) joinURL(code string) string {
	return "choreo://join?s=" + url.QueryEscape(h.publicURL) +
		"&c=" + url.QueryEscape(auth.NormaliseCode(code))
}

type redeemRequest struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name"`
}

type redeemResponse struct {
	HouseholdID string `json:"household_id"`
	MemberID    string `json:"member_id"`
	Role        string `json:"role"`
}

// redeemInvite joins the caller to a household.
//
// Authenticated, which is what removes anonymous brute force: fifty bits is
// guessable in theory, but not by someone who must first hold an account and
// pass a bucket of ten attempts an hour.
func (h *handlers) redeemInvite(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if !decode(w, r, &req) {
		return
	}

	ctx := r.Context()
	session := caller(ctx)

	if !allow(w, h.limits.Redeem, session.AccountID.String()) {
		return
	}

	digest := auth.CodeDigest(auth.NormaliseCode(req.Code))

	invite, err := h.store.Identity.InviteByCode(ctx, digest)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	member, err := h.store.AcceptInvite(
		ctx, invite.HouseholdID, invite.ID, session.AccountID, req.DisplayName)
	if err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, redeemResponse{
		HouseholdID: invite.HouseholdID.String(),
		MemberID:    member.ID.String(),
		Role:        member.Role,
	})
}

// revokeDevice removes one of the caller's own installations.
//
// The targeted device gets device_revoked on its next call and wipes its local
// replica — which is the difference between "I am signing out" and "this phone
// is not mine any more".
func (h *handlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	deviceID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{Error: codeNotFound})
		return
	}

	ctx := r.Context()

	if err := h.store.Identity.RevokeDevice(ctx, caller(ctx).AccountID, deviceID); err != nil {
		writeError(w, r, h.logger, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
