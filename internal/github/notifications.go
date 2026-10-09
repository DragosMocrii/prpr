package github

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// notificationsPageSize caps one notifications read. A full page counts as
// a change worth fetching for, since threads past it are not read.
const notificationsPageSize = 50

// NotificationStatus classifies a notifications read.
type NotificationStatus int

const (
	// NotificationsChanged is a 200: Threads changed since the read's since.
	NotificationsChanged NotificationStatus = iota
	// NotificationsNotModified is a 304: nothing changed since LastModified.
	NotificationsNotModified
	// NotificationsTransient is a failed read that may succeed later.
	NotificationsTransient
	// NotificationsRateLimited is a read GitHub refused for RetryAfter.
	NotificationsRateLimited
	// NotificationsUnsupported is a token that cannot read notifications.
	NotificationsUnsupported
)

// NotificationThread is what prpr reads of a notification thread.
type NotificationThread struct {
	Reason      string
	SubjectType string
	Repository  string
	// Number is a pull request's number; 0 for other subjects.
	Number    int
	UpdatedAt time.Time
}

// Notifications is a notifications read's answer.
type Notifications struct {
	Status       NotificationStatus
	LastModified string
	// Date is GitHub's clock when it answered; zero when not sent.
	Date         time.Time
	PollInterval time.Duration
	// RetryAfter is how long a rate-limited read waits, by GitHub's clock.
	RetryAfter time.Duration
	Threads    []NotificationThread
	// Full reports a full page: more threads may have changed.
	Full bool
	// Err says why a transient read failed.
	Err error
}

// Notifications reads the viewer's notification threads updated after
// since, read ones included, with lastModified as If-Modified-Since when
// known. A zero since is a baseline read of one thread, for its
// Last-Modified and Date. gh exits 1 on a 304, so the answer comes from
// the -i output whatever gh's exit status.
func (c *Client) Notifications(ctx context.Context, since time.Time, lastModified string) Notifications {
	path := "notifications?all=true&per_page="
	if since.IsZero() {
		path += "1"
	} else {
		path += strconv.Itoa(notificationsPageSize) + "&since=" + since.UTC().Format(time.RFC3339)
	}
	args := []string{"api", "--hostname", "github.com", "-i"}
	if lastModified != "" {
		args = append(args, "-H", "If-Modified-Since: "+lastModified)
	}
	args = append(args, path)
	data, err := c.response(ctx, "GitHub notifications read failed", args...)
	n, ok := parseNotifications(data)
	if !ok {
		if err == nil {
			err = errors.New("GitHub notifications read returned no HTTP status")
		}
		return Notifications{Status: NotificationsTransient, Err: err}
	}
	return n
}

// parseNotifications reads gh api -i output: a status line, headers, a
// blank line, then the body. ok is false without a status line.
func parseNotifications(data []byte) (Notifications, bool) {
	reader := bufio.NewReader(bytes.NewReader(data))
	line, _ := reader.ReadString('\n')
	proto, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	code, _, _ := strings.Cut(rest, " ")
	status, err := strconv.Atoi(code)
	if !strings.HasPrefix(proto, "HTTP/") || err != nil {
		return Notifications{}, false
	}
	mime, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return Notifications{}, false
	}
	header := http.Header(mime)
	body, _ := io.ReadAll(reader)
	n := Notifications{LastModified: header.Get("Last-Modified")}
	if date, err := http.ParseTime(header.Get("Date")); err == nil {
		n.Date = date.UTC()
	}
	if seconds, err := strconv.Atoi(header.Get("X-Poll-Interval")); err == nil && seconds > 0 {
		n.PollInterval = time.Duration(seconds) * time.Second
	}
	switch {
	case status == http.StatusOK:
		threads, err := decodeThreads(body)
		if err != nil {
			return Notifications{Status: NotificationsTransient, Err: err}, true
		}
		n.Status, n.Threads, n.Full = NotificationsChanged, threads, len(threads) >= notificationsPageSize
	case status == http.StatusNotModified:
		n.Status = NotificationsNotModified
	// A secondary limit is a 403 with Retry-After while calls remain.
	case status == http.StatusTooManyRequests || status == http.StatusForbidden && (header.Get("X-RateLimit-Remaining") == "0" || retryAfter(header, n.Date) > 0):
		n.Status, n.RetryAfter = NotificationsRateLimited, retryAfter(header, n.Date)
	case status == http.StatusForbidden || status == http.StatusNotFound:
		n.Status = NotificationsUnsupported
	default:
		n.Status, n.Err = NotificationsTransient, fmt.Errorf("GitHub notifications read answered HTTP %d", status)
	}
	return n, true
}

// retryAfter is how long a rate-limited read waits: Retry-After, else
// X-RateLimit-Reset less Date, both GitHub's clock; 0 when neither is known.
func retryAfter(header http.Header, date time.Time) time.Duration {
	if seconds, err := strconv.Atoi(header.Get("Retry-After")); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && !date.IsZero() {
		return max(time.Unix(reset, 0).Sub(date), 0)
	}
	return 0
}

func decodeThreads(body []byte) ([]NotificationThread, error) {
	var raw []struct {
		Reason     string    `json:"reason"`
		UpdatedAt  time.Time `json:"updated_at"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Subject struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode GitHub notifications: %w", err)
	}
	threads := make([]NotificationThread, 0, len(raw))
	for _, t := range raw {
		thread := NotificationThread{Reason: t.Reason, SubjectType: t.Subject.Type, Repository: t.Repository.FullName, UpdatedAt: t.UpdatedAt}
		if t.Subject.Type == "PullRequest" {
			thread.Number = pullNumber(t.Subject.URL, t.Repository.FullName)
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

// pullNumber reads the number from a pull request's API URL,
// https://api.github.com/repos/<repository>/pulls/<n>, ignoring case in
// the repository; 0 for any other URL.
func pullNumber(url, repository string) int {
	prefix := strings.ToLower("https://api.github.com/repos/" + repository + "/pulls/")
	if !strings.HasPrefix(strings.ToLower(url), prefix) {
		return 0
	}
	n, err := strconv.Atoi(url[len(prefix):])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
