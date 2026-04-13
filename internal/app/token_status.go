package app

import (
	"time"

	"github.com/clipboardriver/cb_river_server/internal/model"
)

type enrollmentTokenStatus string

const (
	enrollmentTokenStatusActive    enrollmentTokenStatus = "active"
	enrollmentTokenStatusRevoked   enrollmentTokenStatus = "revoked"
	enrollmentTokenStatusExpired   enrollmentTokenStatus = "expired"
	enrollmentTokenStatusExhausted enrollmentTokenStatus = "exhausted"
)

func resolveEnrollmentTokenStatus(token model.EnrollmentToken, now time.Time) enrollmentTokenStatus {
	if token.RevokedAt != nil {
		return enrollmentTokenStatusRevoked
	}
	if token.ExpiresAt != nil && token.ExpiresAt.Before(now) {
		return enrollmentTokenStatusExpired
	}
	if token.MaxUses > 0 && token.UsedCount >= token.MaxUses {
		return enrollmentTokenStatusExhausted
	}
	return enrollmentTokenStatusActive
}
