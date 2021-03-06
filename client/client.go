package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"

	"github.com/google/go-querystring/query"
	"github.com/juju/errors"
	"github.com/metcalf/saypi/app"
	"github.com/metcalf/saypi/auth"
	"github.com/metcalf/saypi/say"
	"github.com/metcalf/saypi/usererrors"

	"goji.io/pattern"
)

// Route describes an API route for building client requests
type Route interface {
	// HTTPMethods must return exactly one method unless the additional
	// methodsa are only one of OPTIONS and HEAD.
	HTTPMethods() map[string]struct{}

	// URLPath should return the correct path for an API route
	// given a complete list of path variables.
	URLPath(map[pattern.Variable]string) (string, error)
}

// Vars emits a mapping from path variable names to values for
// constructing a URL path with Route.URLPath.
type Vars interface {
	Vars() map[pattern.Variable]string
}

type varmap map[pattern.Variable]string

func (v varmap) Vars() map[pattern.Variable]string { return v }

type Client struct {
	baseURL *url.URL
	do      func(*http.Request) (*http.Response, error)
	auth    string
}

func New(baseURL *url.URL, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		baseURL: baseURL,
		do:      httpClient.Do,
	}
}

func NewForTest(srv http.Handler) *Client {
	base := url.URL{}

	return &Client{
		baseURL: &base,
		do: func(req *http.Request) (*http.Response, error) {
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)

			resp := http.Response{
				Status:        fmt.Sprintf("%d %s", rr.Code, http.StatusText(rr.Code)),
				StatusCode:    rr.Code,
				Body:          ioutil.NopCloser(rr.Body),
				Header:        rr.HeaderMap,
				ContentLength: int64(rr.Body.Len()),
				Request:       req,
			}

			return &resp, nil
		},
	}
}

func (c *Client) NewRequest(rt Route, rtVars Vars, form *url.Values) (*http.Request, error) {
	var vars map[pattern.Variable]string
	if rtVars != nil {
		vars = rtVars.Vars()
	} else {
		vars = make(map[pattern.Variable]string)
	}

	path, err := rt.URLPath(vars)
	if err != nil {
		return nil, errors.Annotate(err, "unable to generate request path")
	}

	rel, err := url.Parse(path)
	if err != nil {
		return nil, errors.Annotatef(err, "unparseable request path %q", path)
	}

	var method string
	var methods []string
	for m := range rt.HTTPMethods() {
		methods = append(methods, m)
	}
	if len(methods) == 0 {
		return nil, errors.New("route does not define any HTTPMethods")
	}
	if len(methods) == 1 {
		method = methods[0]
	} else {
		for _, m := range methods {
			if m != "HEAD" && m != "OPTIONS" {
				if method != "" {
					return nil, errors.Errorf("route defines multiple non-HEAD/OPTIONS methods: %s", methods)
				}
				method = m
			}
		}
	}

	abs := c.baseURL.ResolveReference(rel)

	var body io.Reader
	if form != nil {
		encoded := form.Encode()
		if method == "GET" || method == "HEAD" {
			abs.RawQuery = encoded
		} else {
			body = bytes.NewBufferString(encoded)
		}
	}

	req, err := http.NewRequest(method, abs.String(), body)
	if err != nil {
		return nil, err
	}

	if c.auth != "" {
		req.Header.Add("Authorization", "Bearer "+c.auth)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	return req, nil
}

// Do sends an API request and returns the API response. The API
// response is JSON-decoded and stored in the value pointed to by
// v. If a known usererror response is returned, the error will be a
// UserError with the correct underlying type.
func (c *Client) Do(req *http.Request, v interface{}) (*http.Response, error) {
	rv := reflect.ValueOf(v)
	if !(v == nil || rv.Kind() == reflect.Ptr) {
		return nil, errors.Errorf("value must be a pointer or nil not %s", reflect.TypeOf(v).String())
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode > 399 {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return resp, errors.Annotate(err, "unable to read response body")
		}

		uerr, err := usererrors.UnmarshalJSON(body)
		if err != nil {
			return resp, errors.Annotate(err, "unable to parse error body")
		}
		return resp, uerr
	} else if resp.StatusCode > 299 || resp.StatusCode < 199 {
		return resp, errors.Errorf("unexpected status code %d", resp.StatusCode)
	}

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return resp, errors.Annotate(err, "unable to parse response body")
		}
	}

	return resp, nil
}

func (c *Client) SetAuthorization(auth string) {
	c.auth = auth
}

func (c *Client) execute(rt Route, rtVars Vars, form *url.Values, v interface{}) (*http.Response, error) {
	req, err := c.NewRequest(rt, rtVars, form)
	if err != nil {
		return nil, err
	}

	resp, err := c.Do(req, v)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func (c *Client) CreateUser() (*auth.User, error) {
	var user auth.User

	_, err := c.execute(app.Routes.CreateUser, nil, nil, &user)
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (c *Client) UserExists(id string) (bool, error) {
	resp, err := c.execute(app.Routes.GetUser, &auth.User{ID: id}, nil, nil)
	if _, ok := err.(usererrors.NotFound); ok {
		return false, nil
	} else if err != nil {
		return false, err
	}

	return resp.StatusCode == http.StatusNoContent, nil
}

func (c *Client) GetAnimals() ([]string, error) {
	var animals struct {
		Animals []string `json:"animals"`
	}

	_, err := c.execute(app.Routes.GetAnimals, nil, nil, &animals)
	if err != nil {
		return nil, err
	}

	return animals.Animals, nil
}

// TODO: Needs to be able to reify things and return a value
func (c *Client) ListMoods(params ListParams) *MoodIter {
	return &MoodIter{c.iter(app.Routes.ListMoods, nil, params, say.Mood{})}
}

func (c *Client) SetMood(mood *say.Mood) error {
	form, err := query.Values(mood)

	_, err = c.execute(app.Routes.SetMood, mood, &form, mood)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) GetMood(name string) (*say.Mood, error) {
	mood := say.Mood{Name: name}

	_, err := c.execute(app.Routes.GetMood, &mood, nil, &mood)
	if err != nil {
		return nil, err
	}

	return &mood, nil
}

func (c *Client) DeleteMood(name string) error {
	_, err := c.execute(app.Routes.DeleteMood, &say.Mood{Name: name}, nil, nil)
	if err != nil {
		return err
	}

	return nil
}
