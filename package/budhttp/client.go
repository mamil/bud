package budhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	"github.com/livebud/bud/package/js"
	"github.com/livebud/bud/package/virtual"

	"github.com/livebud/bud/framework/view/ssr"
	"github.com/livebud/bud/internal/urlx"
	"github.com/livebud/bud/package/log"
	"github.com/livebud/bud/package/socket"
)

type Client interface {
	Publish(topic string, data []byte) error
	Open(name string) (fs.File, error)
	js.VM
}

// Try tries loading a dev client from an environment variable or returns an
// empty client if no environment variable is set
func Try(log log.Log, addr string) (Client, error) {
	if addr == "" {
		return discard{}, nil
	}
	return Load(log, addr)
}

// Load a client from an address
func Load(log log.Log, addr string) (Client, error) {
	url, err := urlx.Parse(addr)
	if err != nil {
		return nil, err
	}
	transport, err := socket.Transport(addr)
	if err != nil {
		return nil, fmt.Errorf("budhttp: unable to create transport from listener. %w", err)
	}
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &client{
		baseURL:    url.String(),
		httpClient: httpClient,
		log:        log,
	}, nil
}

type client struct {
	baseURL    string
	httpClient *http.Client
	log        log.Log
}

var _ Client = (*client)(nil)

// Render a path with props on the dev server
func (c *client) Render(route string, props interface{}) (*ssr.Response, error) {
	script, err := fs.ReadFile(c, "bud/view/_ssr.js")
	if err != nil {
		return nil, fmt.Errorf("budhttp: render %q. %w", route, err)
	}
	propBytes, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("budhttp: render %q. %w", route, err)
	}
	expr := fmt.Sprintf(`%s; bud.render(%q, %s)`, script, route, propBytes)
	result, err := c.Eval("_ssr.js", expr)
	if err != nil {
		return nil, fmt.Errorf("budhttp: render %q. %w", route, err)
	}
	var response ssr.Response
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		return nil, fmt.Errorf("budhttp: render %q. %w", route, err)
	}
	return &response, nil
}

func (c *client) Open(name string) (fs.File, error) {
	res, err := c.httpClient.Get(c.baseURL + "/open/" + name)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("budhttp: open %q. %w", name, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("budhttp: open returned unexpected %d. %s", res.StatusCode, body)
	}
	return virtual.UnmarshalJSON(body)
}

type Event struct {
	Topic string `json:"topic,omitempty"`
	Data  []byte `json:"data,omitempty"`
}

func (c *client) Publish(topic string, data []byte) error {
	body, err := json.Marshal(Event{topic, data})
	if err != nil {
		return err
	}
	url := c.baseURL + "/bud/events"
	res, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("budhttp: send returned unexpected %d. %s", res.StatusCode, resBody)
	}
	return nil
}

type Script struct {
	Path   string
	Script string
}

func (c *client) Script(path, script string) error {
	body, err := json.Marshal(Script{path, script})
	if err != nil {
		return err
	}
	url := c.baseURL + "/js/script"
	res, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("budhttp: script returned unexpected %d. %s", res.StatusCode, resBody)
	}
	return nil
}

type Eval struct {
	Path string
	Expr string
}

func (c *client) Eval(path, expr string) (string, error) {
	body, err := json.Marshal(Eval{path, expr})
	if err != nil {
		return "", err
	}
	url := c.baseURL + "/js/eval"
	res, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("budhttp: eval returned unexpected %d. %s", res.StatusCode, resBody)
	}
	return string(resBody), nil
}
