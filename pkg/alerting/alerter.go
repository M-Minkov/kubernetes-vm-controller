package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/node-lifecycle-manager/pkg/config"
	"github.com/slack-go/slack"
	"k8s.io/klog/v2"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Alert struct {
	Severity  Severity
	Title     string
	Message   string
	Labels    map[string]string
	Timestamp time.Time
}

type Alerter struct {
	config     config.AlertingConfig
	httpClient *http.Client
}

func NewAlerter(cfg config.AlertingConfig) *Alerter {
	return &Alerter{
		config:     cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *Alerter) Send(alert Alert) {
	if !a.config.Enabled {
		return
	}

	if alert.Timestamp.IsZero() {
		alert.Timestamp = time.Now()
	}

	if a.config.SlackURL != "" {
		if err := a.sendSlack(alert); err != nil {
			klog.Errorf("failed to send slack alert: %v", err)
		}
	}

	for _, url := range a.config.WebhookURLs {
		if err := a.sendWebhook(url, alert); err != nil {
			klog.Errorf("failed to send webhook alert to %s: %v", url, err)
		}
	}
}

func (a *Alerter) sendSlack(alert Alert) error {
	color := "#36a64f"
	switch alert.Severity {
	case SeverityWarning:
		color = "#ff9800"
	case SeverityCritical:
		color = "#f44336"
	}

	attachment := slack.Attachment{
		Color:      color,
		Title:      alert.Title,
		Text:       alert.Message,
		Footer:     "Node Lifecycle Manager",
		Ts:         json.Number(fmt.Sprintf("%d", alert.Timestamp.Unix())),
	}

	var fields []slack.AttachmentField
	for k, v := range alert.Labels {
		fields = append(fields, slack.AttachmentField{
			Title: k,
			Value: v,
			Short: true,
		})
	}
	attachment.Fields = fields

	msg := slack.WebhookMessage{
		Channel:     a.config.SlackChannel,
		Attachments: []slack.Attachment{attachment},
	}

	return slack.PostWebhook(a.config.SlackURL, &msg)
}

func (a *Alerter) sendWebhook(url string, alert Alert) error {
	payload := map[string]interface{}{
		"severity":  alert.Severity,
		"title":     alert.Title,
		"message":   alert.Message,
		"labels":    alert.Labels,
		"timestamp": alert.Timestamp.Format(time.RFC3339),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("bad response: %d", resp.StatusCode)
	}

	return nil
}
