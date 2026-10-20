package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tingdahl/goutils/config"
)

const (
	defaultScalewayBaseURL = "https://api.scaleway.com"
	defaultScalewayTimeout = 30 * time.Second
)

// ScalewaySender implements Sender using Scaleway's Transactional Email (TEM) REST API.
type ScalewaySender struct {
	projectID  string
	secretKey  string
	region     string
	baseURL    string
	overrideTo string
	httpClient *http.Client
}

// ScalewayOption configures a ScalewaySender.
type ScalewayOption func(*ScalewaySender)

// WithProjectID sets the Scaleway Project ID (UUID).
func WithProjectID(projectID string) ScalewayOption {
	return func(s *ScalewaySender) {
		s.projectID = strings.TrimSpace(projectID)
	}
}

// WithSecretKey sets the Scaleway API secret key.
func WithSecretKey(secretKey string) ScalewayOption {
	return func(s *ScalewaySender) {
		s.secretKey = strings.TrimSpace(secretKey)
	}
}

// WithRegion sets the Scaleway region (e.g. "fr-par", "nl-ams").
func WithRegion(region string) ScalewayOption {
	return func(s *ScalewaySender) {
		s.region = strings.TrimSpace(region)
	}
}

// WithBaseURL sets the base URL for the Scaleway API (useful for testing).
func WithBaseURL(baseURL string) ScalewayOption {
	return func(s *ScalewaySender) {
		s.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

// WithOverrideTo sets an instance-level recipient override address.
// If set, this takes precedence over the global override address.
func WithOverrideTo(overrideTo string) ScalewayOption {
	return func(s *ScalewaySender) {
		s.overrideTo = strings.TrimSpace(overrideTo)
	}
}

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(client *http.Client) ScalewayOption {
	return func(s *ScalewaySender) {
		if client != nil {
			s.httpClient = client
		}
	}
}

// WithConfig loads settings from the provided ConfigManager.
func WithConfig(cm *config.ConfigManager) ScalewayOption {
	return func(s *ScalewaySender) {
		if cm == nil {
			return
		}
		if s.projectID == "" {
			s.projectID = cm.GetSCWProjectID()
		}
		if s.secretKey == "" {
			s.secretKey = cm.GetSCWSecretKey()
		}
		if s.region == "" {
			s.region = cm.GetSCWRegion()
		}
		if s.overrideTo == "" {
			s.overrideTo = cm.GetEmailOverrideTo()
		}
	}
}

// NewScalewaySender creates a new ScalewaySender initialized with the provided options.
// If options are omitted, credentials and region are loaded from goutils/config defaults.
func NewScalewaySender(opts ...ScalewayOption) (*ScalewaySender, error) {
	sender := &ScalewaySender{
		baseURL: defaultScalewayBaseURL,
		httpClient: &http.Client{
			Timeout: defaultScalewayTimeout,
		},
	}

	// Load defaults from config singleton
	WithConfig(config.Config())(sender)

	// Apply explicit options
	for _, opt := range opts {
		if opt != nil {
			opt(sender)
		}
	}

	if sender.secretKey == "" {
		return nil, errors.New("scaleway email: secret key is required (set SCW_SECRET_KEY or use WithSecretKey)")
	}
	if sender.projectID == "" {
		return nil, errors.New("scaleway email: project ID is required (set SCW_PROJECT_ID or use WithProjectID)")
	}
	if sender.region == "" {
		sender.region = config.DefaultSCWRegion
	} else {
		sender.region = config.NormalizeSCWRegion(sender.region)
	}
	if sender.baseURL == "" {
		sender.baseURL = defaultScalewayBaseURL
	}
	if sender.httpClient == nil {
		sender.httpClient = &http.Client{Timeout: defaultScalewayTimeout}
	}

	return sender, nil
}

// Scaleway API JSON payload structures
type scwAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type scwAttachment struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"` // Base64 encoded
}

type scwHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type scwCreateEmailRequest struct {
	ProjectID         string          `json:"project_id"`
	From              scwAddress      `json:"from"`
	To                []scwAddress    `json:"to"`
	CC                []scwAddress    `json:"cc,omitempty"`
	BCC               []scwAddress    `json:"bcc,omitempty"`
	Subject           string          `json:"subject"`
	Text              string          `json:"text,omitempty"`
	HTML              string          `json:"html,omitempty"`
	Attachments       []scwAttachment `json:"attachments,omitempty"`
	AdditionalHeaders []scwHeader     `json:"additional_headers,omitempty"`
}

type scwErrorResponse struct {
	Message     string `json:"message"`
	Type        string `json:"type"`
	HelpMessage string `json:"help_message"`
}

// Send sends an email message via Scaleway's Transactional Email API.
func (s *ScalewaySender) Send(ctx context.Context, msg *Message) error {
	if msg == nil {
		return ErrNilMessage
	}

	// Determine override address: instance setting takes precedence over global setting.
	override := s.overrideTo
	if override == "" {
		override = GetGlobalOverrideTo()
	}

	// Apply override if configured: drops all CC and BCC, replaces To with override address.
	effectiveMsg := msg.ApplyOverride(override)

	// Validate effective message
	if err := effectiveMsg.Validate(); err != nil {
		return err
	}

	// Build Scaleway payload
	reqPayload := scwCreateEmailRequest{
		ProjectID: s.projectID,
		From: scwAddress{
			Email: effectiveMsg.From.Email,
			Name:  effectiveMsg.From.Name,
		},
		Subject: effectiveMsg.Subject,
		Text:    effectiveMsg.Text,
		HTML:    effectiveMsg.HTML,
	}

	for _, to := range effectiveMsg.To {
		reqPayload.To = append(reqPayload.To, scwAddress{Email: to.Email, Name: to.Name})
	}
	for _, cc := range effectiveMsg.CC {
		reqPayload.CC = append(reqPayload.CC, scwAddress{Email: cc.Email, Name: cc.Name})
	}
	for _, bcc := range effectiveMsg.BCC {
		reqPayload.BCC = append(reqPayload.BCC, scwAddress{Email: bcc.Email, Name: bcc.Name})
	}

	for _, att := range effectiveMsg.Attachments {
		if len(att.Content) == 0 {
			continue
		}
		reqPayload.Attachments = append(reqPayload.Attachments, scwAttachment{
			Name:    att.Name,
			Type:    att.ContentType(),
			Content: base64.StdEncoding.EncodeToString(att.Content),
		})
	}

	// Handle Reply-To and custom headers
	hasReplyTo := false
	if effectiveMsg.ReplyTo != nil {
		reqPayload.AdditionalHeaders = append(reqPayload.AdditionalHeaders, scwHeader{
			Key:   "Reply-To",
			Value: effectiveMsg.ReplyTo.String(),
		})
		hasReplyTo = true
	}

	for k, v := range effectiveMsg.Headers {
		if strings.EqualFold(k, "Reply-To") && hasReplyTo {
			continue
		}
		reqPayload.AdditionalHeaders = append(reqPayload.AdditionalHeaders, scwHeader{
			Key:   k,
			Value: v,
		})
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("scaleway email: failed to serialize request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/transactional-email/v1alpha1/regions/%s/emails", s.baseURL, s.region)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("scaleway email: failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", s.secretKey)
	req.Header.Set("User-Agent", "goutils-email")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("scaleway email: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Error response from Scaleway
	body, _ := io.ReadAll(resp.Body)
	var scwErr scwErrorResponse
	if jsonErr := json.Unmarshal(body, &scwErr); jsonErr == nil && scwErr.Message != "" {
		if scwErr.HelpMessage != "" {
			return fmt.Errorf("scaleway email api error (status %d): %s (help: %s)", resp.StatusCode, scwErr.Message, scwErr.HelpMessage)
		}
		return fmt.Errorf("scaleway email api error (status %d): %s", resp.StatusCode, scwErr.Message)
	}

	return fmt.Errorf("scaleway email api error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
