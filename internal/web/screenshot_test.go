package web

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Screenshots of the real page, on request.
//
// `AUCOM/AUE/AUB 246I1.1` asked for images, not only DOM assertions, of the
// Build & Run journey. The driver already knows WHEN a state is worth showing;
// what it cannot do from inside the page is photograph it. So the browser is
// started with `--remote-debugging-pipe` — the DevTools protocol over two
// inherited file descriptors, NUL-delimited JSON, no port and no websocket —
// and the driver asks the test to capture through it.
//
// It is OFF unless AUCOM_JOURNEY_SCREENSHOTS names a directory. Off, the
// driver's snap requests answer at once and the browser is started exactly as
// before, so the default test run is unchanged.

const screenshotEnv = "AUCOM_JOURNEY_SCREENSHOTS"

// cdpPipe is a synchronous DevTools client. One call at a time: the driver
// awaits each snap before moving on.
type cdpPipe struct {
	mu      sync.Mutex
	toCh    *os.File
	fromCh  *bufio.Reader
	next    int
	session string
	origin  string
}

type cdpReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *cdpPipe) call(method string, params any, session string) (json.RawMessage, error) {
	c.next++
	id := c.next
	message := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		message["sessionId"] = session
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if _, err = c.toCh.Write(append(encoded, 0)); err != nil {
		return nil, err
	}
	for {
		raw, readErr := c.fromCh.ReadBytes(0)
		if readErr != nil {
			return nil, readErr
		}
		var reply cdpReply
		if err = json.Unmarshal(raw[:len(raw)-1], &reply); err != nil {
			return nil, err
		}
		if reply.ID != id {
			continue // an event, or an answer nobody is waiting for
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, reply.Error.Message)
		}

		return reply.Result, nil
	}
}

// attach finds the journey's page and opens a flat session on it, once.
func (c *cdpPipe) attach() error {
	if c.session != "" {
		return nil
	}
	raw, err := c.call("Target.getTargets", map[string]any{}, "")
	if err != nil {
		return err
	}
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err = json.Unmarshal(raw, &targets); err != nil {
		return err
	}
	for _, target := range targets.TargetInfos {
		if target.Type != "page" || !strings.HasPrefix(target.URL, c.origin) {
			continue
		}
		raw, err = c.call("Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, "")
		if err != nil {
			return err
		}
		var attached struct {
			SessionID string `json:"sessionId"`
		}
		if err = json.Unmarshal(raw, &attached); err != nil {
			return err
		}
		c.session = attached.SessionID

		return nil
	}

	return fmt.Errorf("no page at %s among %d target(s)", c.origin, len(targets.TargetInfos))
}

// capture writes the page's viewport, as a PNG, to path.
func (c *cdpPipe) capture(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.attach(); err != nil {
		return err
	}
	raw, err := c.call("Page.captureScreenshot", map[string]any{"format": "png"}, c.session)
	if err != nil {
		return err
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err = json.Unmarshal(raw, &shot); err != nil {
		return err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}

	return os.WriteFile(path, png, 0o644)
}

// screenshotter is what the drive handler calls for /journey/snap.
type screenshotter struct {
	pipe   *cdpPipe
	dir    string
	prefix string
	mu     sync.Mutex
	count  int
	taken  []string
}

var snapName = regexp.MustCompile(`[^a-z0-9-]+`)

func (s *screenshotter) snap(name string) error {
	s.mu.Lock()
	s.count++
	file := fmt.Sprintf("%s-%02d-%s.png", s.prefix, s.count,
		strings.Trim(snapName.ReplaceAllString(strings.ToLower(name), "-"), "-"))
	s.mu.Unlock()
	path := filepath.Join(s.dir, file)
	if err := s.pipe.capture(path); err != nil {
		return err
	}
	s.mu.Lock()
	s.taken = append(s.taken, file)
	s.mu.Unlock()

	return nil
}
