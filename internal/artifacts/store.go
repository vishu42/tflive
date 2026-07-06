package artifacts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vishu42/megagega/internal/traits"
)

const logContentType = "text/plain; charset=utf-8"

type ObjectStore interface {
	PutObject(ctx context.Context, key string, contentType string, body io.Reader) error
	GetObject(ctx context.Context, key string) ([]byte, error)
}

type LogMetadataRecorder interface {
	RecordTemplateRunLog(ctx context.Context, log traits.TemplateRunLog) error
}

type LogStore struct {
	store    ObjectStore
	recorder LogMetadataRecorder
	now      func() time.Time
}

func NewLogStore(store ObjectStore) LogStore {
	return LogStore{store: store, now: time.Now}
}

func NewRecordedLogStore(store ObjectStore, recorder LogMetadataRecorder) LogStore {
	return LogStore{store: store, recorder: recorder, now: time.Now}
}

func (store LogStore) PutTemplateRunLog(ctx context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string, body io.Reader) error {
	key, err := LogKey(tenantID, runID, phase)
	if err != nil {
		return err
	}
	content, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read template run log: %w", err)
	}
	if err := store.store.PutObject(ctx, key, logContentType, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("put template run log: %w", err)
	}
	if store.recorder != nil {
		now := store.now
		if now == nil {
			now = time.Now
		}
		log := traits.TemplateRunLog{
			TenantID:    tenantID,
			RunID:       runID,
			Phase:       phase,
			ObjectKey:   key,
			ContentType: logContentType,
			SizeBytes:   int64(len(content)),
			UploadedAt:  now().UTC(),
		}
		if err := store.recorder.RecordTemplateRunLog(ctx, log); err != nil {
			return fmt.Errorf("record template run log metadata: %w", err)
		}
	}
	return nil
}

func (store LogStore) ReadTemplateRunLog(ctx context.Context, log traits.TemplateRunLog) ([]byte, error) {
	content, err := store.store.GetObject(ctx, log.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("get template run log: %w", err)
	}
	return content, nil
}

func LogKey(tenantID traits.TenantID, runID traits.TemplateRunID, phase string) (string, error) {
	if !safePathComponent(string(tenantID)) || !safePathComponent(string(runID)) || !safePathComponent(phase) {
		return "", fmt.Errorf("tenant ID, run ID, and phase must be safe path components")
	}
	return path.Join("tenants", string(tenantID), "runs", string(runID), "logs", phase+".log"), nil
}

type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) FilesystemStore {
	return FilesystemStore{root: root}
}

func (store FilesystemStore) PutObject(_ context.Context, key string, _ string, body io.Reader) error {
	objectPath, err := store.objectPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o700); err != nil {
		return fmt.Errorf("create object directory: %w", err)
	}
	file, err := os.OpenFile(objectPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open object for write: %w", err)
	}
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write object: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close object: %w", closeErr)
	}
	return nil
}

func (store FilesystemStore) GetObject(_ context.Context, key string) ([]byte, error) {
	objectPath, err := store.objectPath(key)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(objectPath)
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return content, nil
}

func (store FilesystemStore) objectPath(key string) (string, error) {
	if strings.TrimSpace(store.root) == "" {
		return "", fmt.Errorf("artifact store root is required")
	}
	if !safeObjectKey(key) {
		return "", fmt.Errorf("object key must be a safe object key")
	}
	return filepath.Join(append([]string{store.root}, strings.Split(key, "/")...)...), nil
}

type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

type S3Store struct {
	cfg        S3Config
	endpoint   *url.URL
	httpClient *http.Client
	now        func() time.Time
}

func NewS3Store(cfg S3Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	if strings.TrimSpace(cfg.Region) == "" {
		return nil, fmt.Errorf("s3 region is required")
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.SecretAccessKey) == "" {
		return nil, fmt.Errorf("s3 access key id and secret access key are required")
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse s3 endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("s3 endpoint must include scheme and host")
	}
	return &S3Store{
		cfg:        cfg,
		endpoint:   endpoint,
		httpClient: http.DefaultClient,
		now:        time.Now,
	}, nil
}

func (store *S3Store) PutObject(ctx context.Context, key string, contentType string, body io.Reader) error {
	if !safeObjectKey(key) {
		return fmt.Errorf("object key must be a safe object key")
	}
	content, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read object body: %w", err)
	}
	request, err := store.newRequest(ctx, http.MethodPut, key, contentType, content)
	if err != nil {
		return err
	}
	response, err := store.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("put s3 object: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("put s3 object: status %d", response.StatusCode)
	}
	return nil
}

func (store *S3Store) GetObject(ctx context.Context, key string) ([]byte, error) {
	if !safeObjectKey(key) {
		return nil, fmt.Errorf("object key must be a safe object key")
	}
	request, err := store.newRequest(ctx, http.MethodGet, key, "", nil)
	if err != nil {
		return nil, err
	}
	response, err := store.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("get s3 object: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("get s3 object: %w", os.ErrNotExist)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("get s3 object: status %d", response.StatusCode)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read s3 object: %w", err)
	}
	return content, nil
}

func (store *S3Store) newRequest(ctx context.Context, method string, key string, contentType string, content []byte) (*http.Request, error) {
	objectURL := store.objectURL(key)
	var body io.Reader
	if content != nil {
		body = bytes.NewReader(content)
	}
	request, err := http.NewRequestWithContext(ctx, method, objectURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create s3 request: %w", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	store.sign(request, content)
	return request, nil
}

func (store *S3Store) objectURL(key string) url.URL {
	objectURL := *store.endpoint
	escapedKey := escapePath(key)
	if store.cfg.ForcePathStyle {
		objectURL.Path = joinURLPath(objectURL.Path, store.cfg.Bucket, escapedKey)
		return objectURL
	}
	objectURL.Host = store.cfg.Bucket + "." + objectURL.Host
	objectURL.Path = joinURLPath(objectURL.Path, escapedKey)
	return objectURL
}

func (store *S3Store) sign(request *http.Request, content []byte) {
	now := store.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	payloadHash := sha256Hex(content)
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", amzDate)
	if request.Header.Get("Host") == "" {
		request.Host = request.URL.Host
	}

	canonicalHeaders, signedHeaders := canonicalHeaders(request)
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI(request.URL.EscapedPath()),
		canonicalQueryString(request.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	credentialScope := strings.Join([]string{date, store.cfg.Region, "s3", "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := s3SigningKey(store.cfg.SecretAccessKey, date, store.cfg.Region)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		store.cfg.AccessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	))
}

func canonicalHeaders(request *http.Request) (string, string) {
	headers := map[string]string{
		"host":                 request.URL.Host,
		"x-amz-content-sha256": request.Header.Get("X-Amz-Content-Sha256"),
		"x-amz-date":           request.Header.Get("X-Amz-Date"),
	}
	if contentType := request.Header.Get("Content-Type"); contentType != "" {
		headers["content-type"] = contentType
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte(':')
		builder.WriteString(strings.TrimSpace(headers[key]))
		builder.WriteByte('\n')
	}
	return builder.String(), strings.Join(keys, ";")
}

func canonicalURI(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	return escapedPath
}

func canonicalQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for key, valuesForKey := range values {
		sort.Strings(valuesForKey)
		for _, value := range valuesForKey {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func s3SigningKey(secret string, date string, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), date)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, "s3")
	return hmacSHA256(serviceKey, "aws4_request")
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func escapePath(value string) string {
	segments := strings.Split(value, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func joinURLPath(parts ...string) string {
	joined := path.Join(parts...)
	if strings.HasPrefix(parts[0], "/") {
		return "/" + strings.TrimPrefix(joined, "/")
	}
	return "/" + strings.TrimPrefix(joined, "/")
}

func safePathComponent(component string) bool {
	if strings.TrimSpace(component) == "" {
		return false
	}
	if strings.Contains(component, "/") || strings.Contains(component, "\\") {
		return false
	}
	cleaned := path.Clean(component)
	return cleaned == component && component != "." && component != ".."
}

func safeObjectKey(key string) bool {
	if strings.TrimSpace(key) == "" {
		return false
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return false
	}
	cleaned := path.Clean(key)
	if cleaned != key || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if !safePathComponent(segment) {
			return false
		}
	}
	return true
}
