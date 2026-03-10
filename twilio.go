package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

type Twilio struct {
	AccountSID string
	AuthToken  string
	From       string

	queueLock sync.Mutex
}

func (t *Twilio) QueueCall(to, xmlURL string) (TwilioResponse, error) {
	log.Debugf("Twilio.QueueCall(%v, %v)", to, xmlURL)
	var (
		answer TwilioResponse
		err    error
		req    *http.Request
		resp   *http.Response

		params = url.Values{}
	)

	params.Add("Url", xmlURL)
	params.Add("From", t.From)
	params.Add("To", to)
	req, err = http.NewRequest(
		"POST",
		urlPost,
		strings.NewReader(
			params.Encode(),
		),
	)
	if err != nil {
		return answer, err
	}
	req.SetBasicAuth(t.AccountSID, t.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")

	// lock call queue, only one call scheduling at a time
	t.queueLock.Lock()
	defer t.queueLock.Unlock()

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Error(err)
		return answer, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err)
		return answer, err
	}

	// Check HTTP status before unmarshaling
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Errorf("Twilio API returned status %d: %s", resp.StatusCode, string(body))
		return answer, fmt.Errorf("twilio API error: status %d", resp.StatusCode)
	}

	err = json.Unmarshal(body, &answer)
	if err != nil {
		log.Error(err)
		return answer, err
	}

	return answer, nil
}

func (t *Twilio) GenerateXML(text []string, voice string) ([]byte, error) {
	log.Debugf("Twilio.GenerateXML(%+v, %v)", text, voice)
	var (
		payload = FormatRequest{}
	)

	for _, say := range text {
		payload.Say = append(
			payload.Say,
			FormatSay{
				Text:  say,
				Voice: voice,
			},
		)
	}

	return xml.MarshalIndent(payload, "", "  ")
}

type TwilioResponse struct {
	AccountSid      string      `json:"account_sid"`
	Annotation      interface{} `json:"annotation"`
	AnsweredBy      interface{} `json:"answered_by"`
	APIVersion      string      `json:"api_version"`
	CallerName      interface{} `json:"caller_name"`
	DateCreated     string      `json:"date_created"`
	DateUpdated     string      `json:"date_updated"`
	Direction       string      `json:"direction"`
	Duration        string      `json:"duration"`
	EndTime         string      `json:"end_time"`
	ForwardedFrom   string      `json:"forwarded_from"`
	From            string      `json:"from"`
	FromFormatted   string      `json:"from_formatted"`
	GroupSid        interface{} `json:"group_sid"`
	ParentCallSid   interface{} `json:"parent_call_sid"`
	PhoneNumberSid  string      `json:"phone_number_sid"`
	Price           string      `json:"price"`
	PriceUnit       string      `json:"price_unit"`
	Sid             string      `json:"sid"`
	StartTime       string      `json:"start_time"`
	Status          string      `json:"status"`
	SubresourceUris struct {
		Notifications     string `json:"notifications"`
		Recordings        string `json:"recordings"`
		Feedback          string `json:"feedback"`
		FeedbackSummaries string `json:"feedback_summaries"`
		Payments          string `json:"payments"`
		Events            string `json:"events"`
	} `json:"subresource_uris"`
	To          string      `json:"to"`
	ToFormatted string      `json:"to_formatted"`
	TrunkSid    interface{} `json:"trunk_sid"`
	URI         string      `json:"uri"`
	QueueTime   string      `json:"queue_time"`
}

type FormatSay struct {
	XMLName xml.Name `xml:"Say"`
	Text    string   `xml:",chardata"`
	Voice   string   `xml:"voice,attr,omitempty"`
}

type FormatRequest struct {
	XMLName xml.Name `xml:"Response"`
	Say     []FormatSay
	Text    string `xml:",chardata"`
	Play    string `xml:"Play,omitempty"`
}

type FormatPause struct {
	XMLName xml.Name `xml:"Pause"`
	Text    string   `xml:",chardata"`
	Length  string   `xml:"length,attr"`
}
