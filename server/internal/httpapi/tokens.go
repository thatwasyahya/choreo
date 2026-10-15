package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thatwasyahya/choreo/server/internal/auth"
	"github.com/thatwasyahya/choreo/server/internal/store"
)

// tokenPair is what every successful authentication returns.
//
// Lifetimes travel as seconds rather than timestamps: a client whose clock is
// wrong — and a child's handset is set to whatever they felt like — can still
// work out when to renew.
type tokenPair struct {
	Access           string `json:"access"`
	AccessExpiresIn  int    `json:"access_expires_in"`
	Refresh          string `json:"refresh"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// issue mints and stores a token pair.
//
// rotatedFrom links the new session to the one it replaces, which is what makes
// replay detection possible later: without the chain there is no way to tell a
// lost-response retry from two parties alternating on one token.
func (h *handlers) issue(
	ctx context.Context, accountID, deviceID uuid.UUID, rotatedFrom *uuid.UUID,
) (tokenPair, error) {
	access, accessDigest, err := auth.NewToken()
	if err != nil {
		return tokenPair{}, fmt.Errorf("httpapi: minting an access token: %w", err)
	}

	refresh, refreshDigest, err := auth.NewToken()
	if err != nil {
		return tokenPair{}, fmt.Errorf("httpapi: minting a refresh token: %w", err)
	}

	now := time.Now()
	if _, err := h.store.Identity.CreateSession(ctx, store.NewSession{
		AccountID:    accountID,
		DeviceID:     deviceID,
		AccessHash:   accessDigest,
		RefreshHash:  refreshDigest,
		AccessUntil:  now.Add(accessTTL),
		RefreshUntil: now.Add(refreshTTL),
		RotatedFrom:  rotatedFrom,
	}); err != nil {
		return tokenPair{}, err
	}

	return tokenPair{
		Access:           access,
		AccessExpiresIn:  int(accessTTL.Seconds()),
		Refresh:          refresh,
		RefreshExpiresIn: int(refreshTTL.Seconds()),
	}, nil
}

// resolveDevice returns the device to bind a new session to.
//
// A caller may present the device id it already has, which is what lets a phone
// sign back in without losing its sync cursor and re-downloading the household.
// Anything it presents is checked against its own account: the id is minted by
// the server precisely so that nobody can claim someone else's.
func (h *handlers) resolveDevice(
	ctx context.Context, accountID uuid.UUID, presented string,
	appVersion string, protocol int, platform string,
) (uuid.UUID, error) {
	if presented == "" {
		return h.store.Identity.RegisterDevice(ctx, accountID, appVersion, protocol, platform)
	}

	deviceID, err := uuid.Parse(presented)
	if err != nil {
		// Not a device of ours, same as any other unknown one.
		return uuid.Nil, store.ErrDeviceUnknown
	}

	if err := h.store.Identity.CheckDevice(ctx, deviceID, accountID); err != nil {
		return uuid.Nil, err
	}
	return deviceID, nil
}
