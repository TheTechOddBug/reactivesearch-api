package util

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Appbase Public Key to validate the offline license
var AppbasePublicKey = "f6c7f3e774cc07b73cf97f6a561d940274cd20abd5f64d0ebe6f9ef7a63667f1"

// OfflineGracePeriod is the time duration in days that defines the grace period for expired license.
// Arc would start throwing 402 error when OfflineGracePeriod is passed.
var OfflineGracePeriod = 30 // in days

// expiry time for offline license
var expiryTime time.Time

// GetExpiryTime returns the expiry time
func GetExpiryTime() time.Time {
	return expiryTime
}

// SetTimeValidity sets the expiry time
func SetExpiryTime(time time.Time) {
	expiryTime = time
}

// BillingMiddlewareOffline function to be called for each request when offline billing is used
func BillingMiddlewareOffline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Blacklist subscription routes
		if strings.HasPrefix(r.RequestURI, "/arc/subscription") || strings.HasPrefix(r.RequestURI, "/arc/plan") {
			next.ServeHTTP(w, r)
		} else {
			remainingHours := int(time.Since(GetExpiryTime()).Hours())
			// Positive duration represents that the plan is expired
			if remainingHours > 0 {
				// For an expired license, allow grace period
				if remainingHours < OfflineGracePeriod*24 {
					remainingHoursFromGracePeriod := OfflineGracePeriod*24 - remainingHours
					days := int64(remainingHoursFromGracePeriod / 24)
					hours := int64(remainingHoursFromGracePeriod) % 24
					errorMsg := fmt.Sprintf("Your license key has expired, please contact support@appbase.io - your server will stop processing API requests in %d days, %d hours.", days, hours)
					// throw error so sentry can log
					log.Errorln(errorMsg)
					next.ServeHTTP(w, r)
				} else {
					log.Errorln("Your license key has expired, please contact support@appbase.io")
					// Write an error and stop the handler chain
					WriteBackError(w, "Your license key has expired, please contact support@appbase.io", http.StatusPaymentRequired)
					return
				}
			}
			next.ServeHTTP(w, r)
		}
	})
}
