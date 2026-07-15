package openfga_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestLiveBootstrapVerifyAndDerivedWriteRejection(t *testing.T) {
	if os.Getenv("OPENFGA_INTEGRATION") != "1" {
		t.Skip("set OPENFGA_INTEGRATION=1 to run against OpenFGA")
	}

	cfg, err := openfga.LoadConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StoreName = fmt.Sprintf("tflive-integration-%d", time.Now().UnixNano())
	cfg.StoreID = ""
	cfg.ModelID = ""
	model, err := openfga.ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	client := openfga.NewClient(cfg)

	first, err := openfga.Bootstrap(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := deleteStore(cfg, first.StoreID); err != nil {
			t.Errorf("delete integration store: %v", err)
		}
	})
	second, err := openfga.Bootstrap(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("first = %#v second = %#v", first, second)
	}

	cfg.StoreID = first.StoreID
	cfg.ModelID = first.ModelID
	verified, err := openfga.Verify(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	if verified != first {
		t.Fatalf("verified = %#v first = %#v", verified, first)
	}

	for _, relation := range []string{"can_view", "can_operate", "can_approve", "can_manage_access"} {
		relation := relation
		t.Run(relation, func(t *testing.T) {
			if err := requireRejectedDerivedWrite(cfg, relation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func requireRejectedDerivedWrite(cfg openfga.Config, relation string) error {
	payload := map[string]any{
		"authorization_model_id": cfg.ModelID,
		"writes": map[string]any{
			"tuple_keys": []map[string]string{{
				"user":     "user:direct-write",
				"relation": relation,
				"object":   "stack:example",
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := liveEndpoint(cfg.APIURL, "stores", cfg.StoreID, "write")
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if cfg.APIToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	response, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("direct write to %s returned %s: %s", relation, response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func deleteStore(cfg openfga.Config, storeID string) error {
	request, err := http.NewRequest(http.MethodDelete, liveEndpoint(cfg.APIURL, "stores", storeID), nil)
	if err != nil {
		return err
	}
	if cfg.APIToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	response, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("delete store returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func liveEndpoint(base *url.URL, segments ...string) string {
	clone := *base
	rawPath := strings.TrimRight(clone.EscapedPath(), "/")
	for _, segment := range segments {
		rawPath += "/" + url.PathEscape(segment)
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		panic(err)
	}
	clone.Path = path
	clone.RawPath = rawPath
	return clone.String()
}
