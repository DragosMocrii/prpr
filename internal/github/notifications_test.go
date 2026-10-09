package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// notificationsResponse is gh api -i output: status line, headers, blank
// line, body.
func notificationsResponse(status string, headers []string, body string) []byte {
	return []byte(status + "\r\n" + strings.Join(headers, "\r\n") + "\r\n\r\n" + body)
}

func fakeNotifications(data []byte, err error, args *[]string) *Client {
	return &Client{path: "gh", api: func(_ context.Context, _ string, a ...string) ([]byte, error) {
		if args != nil {
			*args = a
		}
		return data, err
	}}
}

var notificationsSince = time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)

func TestNotificationsReadsChangedThreads(t *testing.T) {
	data := notificationsResponse("HTTP/2.0 200 OK", []string{
		"Date: Fri, 09 Oct 2026 04:01:00 GMT",
		"Last-Modified: Fri, 09 Oct 2026 04:00:30 GMT",
		"X-Poll-Interval: 120",
	}, `[
	 {"reason":"review_requested","updated_at":"2026-10-09T04:00:30Z","repository":{"full_name":"Acme/API"},
	  "subject":{"type":"PullRequest","url":"https://api.github.com/repos/acme/api/pulls/12"}},
	 {"reason":"ci_activity","updated_at":"2026-10-09T04:00:10Z","repository":{"full_name":"acme/web"},
	  "subject":{"type":"CheckSuite","url":null}},
	 {"reason":"mention","updated_at":"2026-10-09T04:00:20Z","repository":{"full_name":"acme/web"},
	  "subject":{"type":"Issue","url":"https://api.github.com/repos/acme/web/issues/3"}}
	]`)
	var args []string
	n := fakeNotifications(data, nil, &args).Notifications(context.Background(), notificationsSince, "Fri, 09 Oct 2026 04:00:00 GMT")
	if n.Status != NotificationsChanged || n.PollInterval != 2*time.Minute || n.Full ||
		n.LastModified != "Fri, 09 Oct 2026 04:00:30 GMT" || !n.Date.Equal(time.Date(2026, 10, 9, 4, 1, 0, 0, time.UTC)) {
		t.Fatalf("notifications = %+v", n)
	}
	want := []NotificationThread{
		{Reason: "review_requested", SubjectType: "PullRequest", Repository: "Acme/API", Number: 12, UpdatedAt: time.Date(2026, 10, 9, 4, 0, 30, 0, time.UTC)},
		{Reason: "ci_activity", SubjectType: "CheckSuite", Repository: "acme/web", UpdatedAt: time.Date(2026, 10, 9, 4, 0, 10, 0, time.UTC)},
		{Reason: "mention", SubjectType: "Issue", Repository: "acme/web", UpdatedAt: time.Date(2026, 10, 9, 4, 0, 20, 0, time.UTC)},
	}
	if len(n.Threads) != len(want) {
		t.Fatalf("threads = %+v", n.Threads)
	}
	for i, got := range n.Threads {
		w := want[i]
		if got.Reason != w.Reason || got.SubjectType != w.SubjectType || got.Repository != w.Repository ||
			got.Number != w.Number || !got.UpdatedAt.Equal(w.UpdatedAt) {
			t.Fatalf("thread %d = %+v, want %+v", i, got, w)
		}
	}
	joined := strings.Join(args, " ")
	if !slices.Contains(args, "-i") || !slices.Contains(args, "If-Modified-Since: Fri, 09 Oct 2026 04:00:00 GMT") ||
		!strings.Contains(joined, "all=true") || !strings.Contains(joined, "since=2026-10-09T04:00:00Z") ||
		!strings.Contains(joined, fmt.Sprintf("per_page=%d", notificationsPageSize)) {
		t.Fatalf("args = %q", args)
	}
}

func TestNotificationsBaselineReadsOneThreadWithoutConditions(t *testing.T) {
	data := notificationsResponse("HTTP/2.0 200 OK", []string{"Date: Fri, 09 Oct 2026 04:01:00 GMT", "Last-Modified: Fri, 09 Oct 2026 03:00:00 GMT"}, `[]`)
	var args []string
	n := fakeNotifications(data, nil, &args).Notifications(context.Background(), time.Time{}, "")
	joined := strings.Join(args, " ")
	if n.Status != NotificationsChanged || strings.Contains(joined, "since=") || strings.Contains(joined, "If-Modified-Since") ||
		!strings.Contains(joined, "per_page=1") {
		t.Fatalf("baseline = %+v, args %q", n, args)
	}
}

func TestNotificationsNotModifiedDespiteGhExitStatus(t *testing.T) {
	// gh exits 1 on a 304 but still prints the status line and headers.
	data := notificationsResponse("HTTP/2.0 304 Not Modified", []string{"Date: Fri, 09 Oct 2026 04:01:00 GMT", "X-Poll-Interval: 60"}, "")
	n := fakeNotifications(data, errors.New("exit status 1"), nil).Notifications(context.Background(), notificationsSince, "x")
	if n.Status != NotificationsNotModified || n.PollInterval != time.Minute || n.Err != nil {
		t.Fatalf("notifications = %+v", n)
	}
}

func TestNotificationsClassifiesFailures(t *testing.T) {
	date := "Date: Fri, 09 Oct 2026 04:01:00 GMT"
	reset := fmt.Sprintf("X-RateLimit-Reset: %d", time.Date(2026, 10, 9, 4, 11, 0, 0, time.UTC).Unix())
	for _, tc := range []struct {
		name   string
		data   []byte
		err    error
		status NotificationStatus
		retry  time.Duration
	}{
		{"rate limited 403", notificationsResponse("HTTP/2.0 403 Forbidden", []string{date, "X-RateLimit-Remaining: 0", reset}, `{}`), errors.New("exit status 1"), NotificationsRateLimited, 10 * time.Minute},
		{"429 retry after", notificationsResponse("HTTP/2.0 429 Too Many Requests", []string{date, "Retry-After: 30"}, `{}`), errors.New("exit status 1"), NotificationsRateLimited, 30 * time.Second},
		{"secondary rate limit 403", notificationsResponse("HTTP/2.0 403 Forbidden", []string{date, "X-RateLimit-Remaining: 4999", "Retry-After: 60"}, `{}`), errors.New("exit status 1"), NotificationsRateLimited, time.Minute},
		{"403 without access", notificationsResponse("HTTP/2.0 403 Forbidden", []string{date, "X-RateLimit-Remaining: 4999"}, `{}`), errors.New("exit status 1"), NotificationsUnsupported, 0},
		{"404", notificationsResponse("HTTP/2.0 404 Not Found", []string{date}, `{}`), errors.New("exit status 1"), NotificationsUnsupported, 0},
		{"401 is the fetch's to report", notificationsResponse("HTTP/2.0 401 Unauthorized", []string{date}, `{}`), errors.New("exit status 1"), NotificationsTransient, 0},
		{"502", notificationsResponse("HTTP/2.0 502 Bad Gateway", []string{date}, ``), errors.New("exit status 1"), NotificationsTransient, 0},
		{"no status line", nil, errors.New("GitHub notifications read failed: dial tcp: offline"), NotificationsTransient, 0},
		{"malformed body", notificationsResponse("HTTP/2.0 200 OK", []string{date}, `{"oops"`), nil, NotificationsTransient, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := fakeNotifications(tc.data, tc.err, nil).Notifications(context.Background(), notificationsSince, "x")
			if n.Status != tc.status || n.RetryAfter != tc.retry {
				t.Fatalf("notifications = %+v", n)
			}
			if tc.status == NotificationsTransient && n.Err == nil {
				t.Fatal("transient read without an error")
			}
		})
	}
}

func TestNotificationsFullPage(t *testing.T) {
	var threads []string
	for i := range notificationsPageSize {
		threads = append(threads, fmt.Sprintf(`{"reason":"subscribed","updated_at":"2026-10-09T04:00:30Z","repository":{"full_name":"acme/web"},"subject":{"type":"Issue","url":"https://api.github.com/repos/acme/web/issues/%d"}}`, i+1))
	}
	data := notificationsResponse("HTTP/2.0 200 OK", []string{"Date: Fri, 09 Oct 2026 04:01:00 GMT"}, "["+strings.Join(threads, ",")+"]")
	if n := fakeNotifications(data, nil, nil).Notifications(context.Background(), notificationsSince, "x"); !n.Full {
		t.Fatalf("full page not reported: %+v", n.Status)
	}
}

func TestNotificationsRunAsThePinnedAccountAndHideTheToken(t *testing.T) {
	client, log := fakeGH(t, true)
	client.UseAccount("alice")
	n := client.Notifications(context.Background(), notificationsSince, "")
	if n.Status != NotificationsTransient || n.Err == nil || strings.Contains(n.Err.Error(), fakeToken) {
		t.Fatalf("notifications = %+v", n)
	}
	if got := calls(t, log); !slices.Contains(got, "token="+fakeToken+" args=api --hostname github.com") {
		t.Fatalf("calls = %q", got)
	}
}
