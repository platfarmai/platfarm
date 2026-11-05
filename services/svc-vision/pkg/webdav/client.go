package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) buildURL(remotePath string) string {
	remotePath = strings.TrimPrefix(strings.TrimSpace(remotePath), "/")
	if remotePath == "" {
		return c.baseURL
	}
	return c.baseURL + "/" + remotePath
}

func (c *Client) newRequest(method, remotePath string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.buildURL(remotePath), body)
	if err != nil {
		return nil, err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

func (c *Client) Check(basePath string) error {
	return c.check(basePath, 60*time.Second)
}

func (c *Client) CheckWithTimeout(basePath string, timeout time.Duration) error {
	return c.check(basePath, timeout)
}

func (c *Client) check(basePath string, timeout time.Duration) error {
	if c.baseURL == "" {
		return fmt.Errorf("WebDAV URL not configured")
	}

	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<propfind xmlns="DAV:">
  <prop>
    <resourcetype/>
    <getcontentlength/>
  </prop>
</propfind>`)

	req, err := c.newRequest("PROPFIND", basePath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")

	client := *c.http
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to WebDAV server: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode == http.StatusMultiStatus || resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusNotFound:

		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("WebDAV authentication failed (401/403), check username/password")
	case resp.StatusCode >= 500:
		return fmt.Errorf("WebDAV server error (HTTP %d)", resp.StatusCode)
	default:
		return fmt.Errorf("unexpected WebDAV response (HTTP %d)", resp.StatusCode)
	}
}

func (c *Client) Delete(remotePath string) error {
	req, err := c.newRequest("DELETE", remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete remote file: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch {
	case resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMultiStatus:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("failed to delete remote file (HTTP %d)", resp.StatusCode)
	}
}

func (c *Client) EnsureDir(remotePath string) error {
	if strings.TrimSpace(remotePath) == "" {
		return nil
	}
	req, err := c.newRequest("MKCOL", remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("MKCOL request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusMethodNotAllowed, http.StatusConflict:

		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("WebDAV authentication failed (HTTP %d)", resp.StatusCode)
	default:
		return fmt.Errorf("failed to create remote directory (HTTP %d): %s", resp.StatusCode, remotePath)
	}
}

func (c *Client) Upload(localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer f.Close()

	if idx := strings.LastIndex(remotePath, "/"); idx > 0 {
		if err := c.EnsureDir(remotePath[:idx]); err != nil {
			return err
		}
	}

	req, err := c.newRequest("PUT", remotePath, f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("PUT upload failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, remotePath)
}

type Entry struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

type propfindResponse struct {
	XMLName   xml.Name `xml:"multistatus"`
	Responses []struct {
		Href     string `xml:"href"`
		Propstat struct {
			Prop struct {
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				GetContentLength string `xml:"getcontentlength"`
				GetLastModified  string `xml:"getlastmodified"`
			} `xml:"prop"`
			Status string `xml:"status"`
		} `xml:"propstat"`
	} `xml:"response"`
}

func (c *Client) List(remotePath string) ([]Entry, error) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<propfind xmlns="DAV:">
  <prop>
    <resourcetype/>
    <getcontentlength/>
    <getlastmodified/>
  </prop>
</propfind>`)

	req, err := c.newRequest("PROPFIND", remotePath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("PROPFIND failed (HTTP %d)", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read PROPFIND response: %w", err)
	}

	var pr propfindResponse
	if err := xml.Unmarshal(raw, &pr); err != nil {
		return nil, fmt.Errorf("failed to parse PROPFIND response: %w", err)
	}

	baseTrim := strings.Trim(strings.TrimSpace(remotePath), "/")

	var entries []Entry
	for _, r := range pr.Responses {
		href := r.Href
		if u, err := url.Parse(href); err == nil && u.Path != "" {
			href = u.Path
		}
		name := path.Base(strings.TrimRight(href, "/"))

		isDir := r.Propstat.Prop.ResourceType.Collection != nil
		selfName := path.Base(baseTrim)
		if name == selfName && isDir {
			continue
		}
		if name == "" || name == "." {
			continue
		}

		entry := Entry{Name: name, IsDir: isDir}
		if r.Propstat.Prop.GetContentLength != "" {
			if n, err := strconv.ParseInt(r.Propstat.Prop.GetContentLength, 10, 64); err == nil {
				entry.Size = n
			}
		}
		if r.Propstat.Prop.GetLastModified != "" {
			for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC3339} {
				if t, err := time.Parse(layout, r.Propstat.Prop.GetLastModified); err == nil {
					entry.ModTime = t
					break
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (c *Client) Get(ctx context.Context, remotePath, rangeHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.buildURL(remotePath), nil)
	if err != nil {
		return nil, err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	streamClient := &http.Client{Timeout: 0}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET read failed: %w", err)
	}
	return resp, nil
}

func (c *Client) TestAndUpload(basePath string) error {
	if err := c.Check(basePath); err != nil {
		return err
	}

	testRemote := strings.TrimPrefix(basePath, "/")
	if testRemote != "" {
		testRemote += "/"
	}
	testRemote += ".ds_write_test"

	tmp := fmt.Sprintf("%s/ds_webdav_test_%d.bin", os.TempDir(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, []byte("ok"), 0644); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if err := c.Upload(tmp, testRemote); err != nil {
		return fmt.Errorf("connection OK but write failed: %w", err)
	}

	if req, err := c.newRequest("DELETE", testRemote, nil); err == nil {
		if resp, err := c.http.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	return nil
}
