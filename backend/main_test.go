package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := migrate(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return &app{db: db, dialect: "sqlite"}
}

func TestCreateLockDefaultsPrice(t *testing.T) {
	application := newTestApp(t)
	body := bytes.NewBufferString(`{"secretText":"secret","unlockAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/locks", body)
	recorder := httptest.NewRecorder()

	application.createLock(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var item lockItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.PriceAmount != defaultPrice {
		t.Fatalf("expected default price %d, got %d", defaultPrice, item.PriceAmount)
	}
	if item.Name != "Lock #"+strconv.FormatInt(item.ID, 10) {
		t.Fatalf("expected default name Lock #%d, got %q", item.ID, item.Name)
	}
	if item.SecretText != "" {
		t.Fatalf("secret text should be hidden before unlock")
	}
}

func TestCreateLockStoresCustomName(t *testing.T) {
	application := newTestApp(t)
	body, _ := json.Marshal(createLockRequest{
		Name:       "Launch note",
		SecretText: "secret",
		UnlockAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	request := httptest.NewRequest(http.MethodPost, "/api/locks", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	application.createLock(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var item lockItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Name != "Launch note" {
		t.Fatalf("expected custom name, got %q", item.Name)
	}
}

func TestUnlockCreatesPurchaseEvent(t *testing.T) {
	application := newTestApp(t)
	price := 900
	body, _ := json.Marshal(createLockRequest{
		SecretText:  "paid secret",
		UnlockAt:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		PriceAmount: &price,
	})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/locks", bytes.NewReader(body))
	createRecorder := httptest.NewRecorder()
	application.createLock(createRecorder, createRequest)

	var created lockItem
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	if _, err := application.recordUnlock(created.ID, "stripe", "cs_test_record", "paid_stripe"); err != nil {
		t.Fatalf("record unlock: %v", err)
	}

	var eventCount int
	var amount int
	if err := application.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(amount), 0) FROM purchase_events WHERE lock_id = ?`, created.ID).Scan(&eventCount, &amount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected one purchase event, got %d", eventCount)
	}
	if amount != price {
		t.Fatalf("expected purchase amount %d, got %d", price, amount)
	}
}

func TestPublicUnlockRouteIsNotAvailable(t *testing.T) {
	application := newTestApp(t)
	request := httptest.NewRequest(http.MethodPost, "/api/locks/1/unlock", nil)
	recorder := httptest.NewRecorder()

	application.handleLockByID(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected unlock route status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateUnlockTimeForTimeOpenedLock(t *testing.T) {
	application := newTestApp(t)
	body, _ := json.Marshal(createLockRequest{
		Name:       "Letter",
		SecretText: "secret",
		UnlockAt:   time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/locks", bytes.NewReader(body))
	createRecorder := httptest.NewRecorder()
	application.createLock(createRecorder, createRequest)

	var created lockItem
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	nextUnlock := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	updateBody, _ := json.Marshal(updateUnlockTimeRequest{UnlockAt: nextUnlock})
	request := httptest.NewRequest(http.MethodPatch, "/api/locks/"+strconv.FormatInt(created.ID, 10), bytes.NewReader(updateBody))
	recorder := httptest.NewRecorder()

	application.handleLockByID(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var updated lockItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Letter" {
		t.Fatalf("expected name to stay unchanged, got %q", updated.Name)
	}
	if updated.SecretText != "" {
		t.Fatal("expected secret text to be hidden after moving unlock time into the future")
	}
	if updated.IsOpen {
		t.Fatal("expected lock to be closed after moving unlock time into the future")
	}
	if updated.UnlockAt != nextUnlock {
		t.Fatalf("expected unlock time %q, got %q", nextUnlock, updated.UnlockAt)
	}
}

func TestUpdateUnlockTimeRejectsStillLockedLock(t *testing.T) {
	application := newTestApp(t)
	body, _ := json.Marshal(createLockRequest{
		SecretText: "secret",
		UnlockAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/locks", bytes.NewReader(body))
	createRecorder := httptest.NewRecorder()
	application.createLock(createRecorder, createRequest)

	var created lockItem
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	nextUnlock := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	updateBody, _ := json.Marshal(updateUnlockTimeRequest{UnlockAt: nextUnlock})
	request := httptest.NewRequest(http.MethodPatch, "/api/locks/"+strconv.FormatInt(created.ID, 10), bytes.NewReader(updateBody))
	recorder := httptest.NewRecorder()

	application.handleLockByID(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected update status 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateUnlockTimeRejectsPaidLock(t *testing.T) {
	application := newTestApp(t)
	body, _ := json.Marshal(createLockRequest{
		SecretText: "paid secret",
		UnlockAt:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	createRequest := httptest.NewRequest(http.MethodPost, "/api/locks", bytes.NewReader(body))
	createRecorder := httptest.NewRecorder()
	application.createLock(createRecorder, createRequest)

	var created lockItem
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if _, err := application.recordUnlock(created.ID, "stripe", "cs_test_relock", "paid_stripe"); err != nil {
		t.Fatalf("record unlock: %v", err)
	}

	nextUnlock := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	updateBody, _ := json.Marshal(updateUnlockTimeRequest{UnlockAt: nextUnlock})
	request := httptest.NewRequest(http.MethodPatch, "/api/locks/"+strconv.FormatInt(created.ID, 10), bytes.NewReader(updateBody))
	recorder := httptest.NewRecorder()

	application.handleLockByID(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected update status 409, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPurchaseHistoryRouteIsNotAvailable(t *testing.T) {
	application := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "/api/purchases", nil)
	recorder := httptest.NewRecorder()

	application.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected purchases route status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestStripeConfigRequiresPaymentsFlag(t *testing.T) {
	t.Setenv("APP_PAYMENTS_ENABLED", "")
	application := newTestApp(t)
	application.stripeKey = "sk_test_123"

	request := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	recorder := httptest.NewRecorder()

	application.handleConfig(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected config status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var config configResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config.StripeEnabled {
		t.Fatal("expected stripe to stay disabled without APP_PAYMENTS_ENABLED")
	}
}

func TestPostgresTargetDescriptionDoesNotExposePassword(t *testing.T) {
	description := postgresTargetDescription("postgresql://postgres.abc:super-secret%21@aws-0-us-east-1.pooler.supabase.com:6543/postgres")

	if strings.Contains(description, "super-secret") {
		t.Fatalf("description exposed password: %s", description)
	}
	if !strings.Contains(description, "user=postgres.abc") {
		t.Fatalf("description did not include user: %s", description)
	}
	if !strings.Contains(description, "host=aws-0-us-east-1.pooler.supabase.com:6543") {
		t.Fatalf("description did not include host: %s", description)
	}
	if !strings.Contains(description, "database=postgres") {
		t.Fatalf("description did not include database: %s", description)
	}
}

func TestStripeCheckoutIsDisabledByDefault(t *testing.T) {
	t.Setenv("APP_PAYMENTS_ENABLED", "")
	application := newTestApp(t)
	application.stripeKey = "sk_test_123"

	request := httptest.NewRequest(http.MethodPost, "/api/locks/1/checkout", nil)
	recorder := httptest.NewRecorder()

	application.handleLockByID(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected checkout status 503, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDevReloadRouteIsHiddenInProduction(t *testing.T) {
	application := newTestApp(t)
	application.devMode = false
	request := httptest.NewRequest(http.MethodGet, "/dev/reload", nil)
	recorder := httptest.NewRecorder()

	application.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected dev reload status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestStripeSignatureVerification(t *testing.T) {
	payload := []byte(`{"id":"evt_test","type":"checkout.session.completed"}`)
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	header := "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))

	if err := verifyStripeSignature(payload, header, secret); err != nil {
		t.Fatalf("expected signature to verify: %v", err)
	}
	if err := verifyStripeSignature(payload, header, "wrong_secret"); err == nil {
		t.Fatal("expected wrong signature secret to fail")
	}
}
