package apis_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func TestDefaultRateLimitMiddleware(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	app.Settings().RateLimits.Enabled = true
	app.Settings().RateLimits.Rules = []core.RateLimitRule{
		{
			Label:       "/rate/",
			MaxRequests: 2,
			Duration:    1,
		},
		{
			Label:       "/rate/b",
			MaxRequests: 3,
			Duration:    1,
		},
		{
			Label:       "POST /rate/b",
			MaxRequests: 1,
			Duration:    1,
		},
		{
			Label:       "/rate/guest",
			MaxRequests: 1,
			Duration:    1,
			Audience:    core.RateLimitRuleAudienceGuest,
		},
		{
			Label:       "/rate/auth",
			MaxRequests: 1,
			Duration:    1,
			Audience:    core.RateLimitRuleAudienceAuth,
		},
	}

	pbRouter, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	pbRouter.GET("/norate", func(e *core.RequestEvent) error {
		return e.String(200, "norate")
	}).BindFunc(func(e *core.RequestEvent) error {
		return e.Next()
	})
	pbRouter.GET("/rate/a", func(e *core.RequestEvent) error {
		return e.String(200, "a")
	})
	pbRouter.GET("/rate/b", func(e *core.RequestEvent) error {
		return e.String(200, "b")
	})
	pbRouter.GET("/rate/guest", func(e *core.RequestEvent) error {
		return e.String(200, "guest")
	})
	pbRouter.GET("/rate/auth", func(e *core.RequestEvent) error {
		return e.String(200, "auth")
	})

	mux, err := pbRouter.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	scenarios := []struct {
		url string

		// freshWindow starts this request at the beginning of a new limiter
		// window, which resets the counter for its rule.
		freshWindow bool

		authenticated  bool
		expectedStatus int
	}{
		{"/norate", false, false, 200},
		{"/norate", false, false, 200},
		{"/norate", false, false, 200},
		{"/norate", false, false, 200},
		{"/norate", false, false, 200},

		// "/rate/" rule, 2 requests per window
		{"/rate/a", true, false, 200},
		{"/rate/a", true, false, 200},
		{"/rate/a", true, false, 200},
		{"/rate/a", false, false, 200},
		{"/rate/a", false, false, 429},
		{"/rate/a", false, false, 429},
		{"/rate/a", true, false, 200},
		{"/rate/a", false, false, 200},
		{"/rate/a", false, false, 429},

		// "/rate/b" rule, 3 requests per window
		{"/rate/b", true, false, 200},
		{"/rate/b", false, false, 200},
		{"/rate/b", false, false, 200},
		{"/rate/b", false, false, 429},
		{"/rate/b", true, false, 200},
		{"/rate/b", false, false, 200},
		{"/rate/b", false, false, 200},
		{"/rate/b", false, false, 429},

		// "auth" with guest (should fallback to the /rate/ rule)
		{"/rate/auth", true, false, 200},
		{"/rate/auth", false, false, 200},
		{"/rate/auth", false, false, 429},
		{"/rate/auth", false, false, 429},

		// "auth" rule with regular user (should match the /rate/auth rule)
		{"/rate/auth", true, true, 200},
		{"/rate/auth", false, true, 429},
		{"/rate/auth", false, true, 429},

		// "guest" with guest (should match the /rate/guest rule)
		{"/rate/guest", true, false, 200},
		{"/rate/guest", false, false, 429},
		{"/rate/guest", false, false, 429},

		// "guest" rule with regular user (should fallback to the /rate/ rule)
		{"/rate/guest", true, true, 200},
		{"/rate/guest", false, true, 200},
		{"/rate/guest", false, true, 429},
		{"/rate/guest", false, true, 429},
	}

	// built once: creating it costs a db round trip and a token signing, and
	// doing that per request would eat into the limiter window the batch is
	// supposed to fit inside
	authRecord, err := app.FindAuthRecordByEmail("users", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	authToken, err := authRecord.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}

	for i, s := range scenarios {
		// the index keeps the subtest names unique, so a failure points at a
		// specific row instead of an anonymous "#06"
		t.Run(fmt.Sprintf("%02d_%s", i, strings.TrimPrefix(s.url, "/")), func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", s.url, nil)

			if s.authenticated {
				req.Header.Add("Authorization", authToken)
			}

			if s.freshWindow {
				waitForFreshRateLimitWindow()
			}

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			if result.StatusCode != s.expectedStatus {
				t.Fatalf("Expected response status %d, got %d", s.expectedStatus, result.StatusCode)
			}
		})
	}
}

// waitForFreshRateLimitWindow blocks until just after the next second boundary.
//
// The limiter uses a fixed window keyed on time.Now().Unix() and the rules in
// these tests use a 1s interval, so crossing a boundary always resets the
// counter. Batches of requests are asserted as a group ("this one passes, the
// next is rejected"), which only holds if the whole batch lands in one window.
// Starting at a boundary gives the batch a full second instead of leaving it to
// chance how much of the current one is left - the previous fixed sleeps left a
// batch straddling a boundary roughly one run in three.
func waitForFreshRateLimitWindow() {
	now := time.Now()
	time.Sleep(time.Until(now.Truncate(time.Second).Add(time.Second)) + 5*time.Millisecond)
}

func TestDefaultRateLimitMiddlewareSkipChecks(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	app.Settings().RateLimits.Enabled = true
	app.Settings().RateLimits.Rules = []core.RateLimitRule{
		{
			Label:       "/rate",
			MaxRequests: 1,
			Duration:    5,
		},
	}

	pbRouter, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}

	// just for the exclude tests - load the user IP from a query param
	pbRouter.Bind(&hook.Handler[*core.RequestEvent]{
		Priority: apis.DefaultRateLimitMiddlewarePriority - 1,
		Func: func(e *core.RequestEvent) error {
			testIp := e.Request.URL.Query().Get("testIP")
			if testIp != "" {
				e.Request.Header.Set("x-test-ip", testIp)
			}

			return e.Next()
		},
	})

	pbRouter.GET("/rate", func(e *core.RequestEvent) error {
		return e.String(200, "test")
	})

	mux, err := pbRouter.BuildMux()
	if err != nil {
		t.Fatal(err)
	}

	checkStatusCodes := func(t *testing.T, got []int, expected []int) {
		if len(expected) != len(got) {
			t.Fatalf("Expected status codes %v, got %v", expected, got)
		}

		for i, item := range expected {
			if got[i] != item {
				t.Fatalf("Expected %d status code to be %d, got %d:\n%v", i, item, got[i], got)
			}
		}
	}

	t.Run("base check", func(t *testing.T) {
		app.Settings().RateLimits.Enabled = true

		statusCodes := []int{}
		for range 3 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/rate", nil)

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			statusCodes = append(statusCodes, result.StatusCode)
		}

		checkStatusCodes(t, statusCodes, []int{200, 429, 429})
	})

	t.Run("disabled rate limiter", func(t *testing.T) {
		app.Settings().RateLimits.Enabled = false

		statusCodes := []int{}
		for range 3 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/rate", nil)

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			statusCodes = append(statusCodes, result.StatusCode)
		}

		checkStatusCodes(t, statusCodes, []int{200, 200, 200})
	})

	t.Run("authenticated as superuser", func(t *testing.T) {
		app.Settings().RateLimits.Enabled = true

		superuser, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
		if err != nil {
			t.Fatal(err)
		}

		token, err := superuser.NewAuthToken()
		if err != nil {
			t.Fatal(err)
		}

		statusCodes := []int{}
		for range 3 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/rate", nil)
			req.Header.Add("Authorization", token)

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			statusCodes = append(statusCodes, result.StatusCode)
		}

		checkStatusCodes(t, statusCodes, []int{200, 200, 200})
	})

	t.Run("excludedIPs (different)", func(t *testing.T) {
		app.Settings().RateLimits.Enabled = true
		app.Settings().RateLimits.ExcludedIPs = []string{"10.0.0.0"}
		app.Settings().TrustedProxy.Headers = []string{"x-test-ip"}

		statusCodes := []int{}
		for range 3 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/rate", nil)
			req.Header.Set("x-test-ip", "127.0.0.1")

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			statusCodes = append(statusCodes, result.StatusCode)
		}

		checkStatusCodes(t, statusCodes, []int{200, 429, 429})
	})

	t.Run("excludedIPs (match)", func(t *testing.T) {
		app.Settings().RateLimits.Enabled = true
		app.Settings().RateLimits.ExcludedIPs = []string{"127.0.0.1"}
		app.Settings().TrustedProxy.Headers = []string{"x-test-ip"}

		statusCodes := []int{}
		for range 3 {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/rate", nil)
			req.Header.Set("x-test-ip", "127.0.0.1")

			mux.ServeHTTP(rec, req)

			result := rec.Result()

			statusCodes = append(statusCodes, result.StatusCode)
		}

		checkStatusCodes(t, statusCodes, []int{200, 200, 200})
	})
}
