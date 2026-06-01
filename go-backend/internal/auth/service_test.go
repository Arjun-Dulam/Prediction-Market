package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSignedTokenAndMiddleware(t *testing.T) {
	service, err := New(nil, "a-development-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.sign("user-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.Parse(token)
	if err != nil || claims.Subject != "user-1" {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	if _, err = service.Parse(token + "tampered"); err == nil {
		t.Fatal("tampered token accepted")
	}

	handler := service.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := UserID(r.Context())
		if !ok || id != "user-1" {
			t.Fatalf("middleware user=%q ok=%v", id, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}
}
