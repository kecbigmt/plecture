package pullquery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature_ValidSignatureVerifies(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	if !VerifySignature("s3cr3t", body, sign("s3cr3t", body)) {
		t.Error("want a valid HMAC to verify")
	}
}

func TestVerifySignature_WrongSecretFails(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	if VerifySignature("s3cr3t", body, sign("wrong-secret", body)) {
		t.Error("want a signature computed with a different secret to fail")
	}
}

func TestVerifySignature_TamperedBodyFails(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	header := sign("s3cr3t", body)
	if VerifySignature("s3cr3t", []byte(`{"action":"closed"}`), header) {
		t.Error("want a signature computed over a different body to fail")
	}
}

func TestVerifySignature_MissingOrMalformedHeaderFails(t *testing.T) {
	body := []byte(`{}`)
	for _, header := range []string{"", "not-a-signature", "sha1=deadbeef", "sha256=not-hex"} {
		if VerifySignature("s3cr3t", body, header) {
			t.Errorf("want header %q to fail verification", header)
		}
	}
}

func TestVerifySignature_EmptySecretFailsClosed(t *testing.T) {
	body := []byte(`{}`)
	if VerifySignature("", body, sign("", body)) {
		t.Error("want an empty secret to fail closed rather than verify")
	}
}

func pullRequestPayload(fullName, htmlURL, state string, draft bool, labels ...string) []byte {
	labelObjs := make([]byte, 0)
	for i, l := range labels {
		if i > 0 {
			labelObjs = append(labelObjs, ',')
		}
		labelObjs = append(labelObjs, []byte(`{"name":"`+l+`"}`)...)
	}
	return []byte(`{"action":"labeled","repository":{"full_name":"` + fullName + `"},"pull_request":{"html_url":"` + htmlURL + `","state":"` + state + `","draft":` + boolStr(draft) + `,"labels":[` + string(labelObjs) + `]}}`)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestMatchPullRequestEvent_MatchingDeliveryProducesItem(t *testing.T) {
	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/7", "open", false, "agent-review")
	in := Inputs{Repositories: []string{"acme/widgets"}, Labels: []string{"agent-review"}, State: "open", Draft: false}
	item, matched, err := MatchPullRequestEvent(body, in)
	if err != nil {
		t.Fatalf("MatchPullRequestEvent: %v", err)
	}
	if !matched {
		t.Fatal("want a match for a delivery whose current state satisfies the query")
	}
	if item.Resource != "https://github.com/acme/widgets/pull/7" || item.Owner != "acme" || item.Repository != "widgets" {
		t.Errorf("item = %+v", item)
	}
}

func TestMatchPullRequestEvent_NonMatchingDeliveryProducesNoItemAndNoError(t *testing.T) {
	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/7", "closed", false, "agent-review")
	in := Inputs{Repositories: []string{"acme/widgets"}, Labels: []string{"agent-review"}, State: "open", Draft: false}
	_, matched, err := MatchPullRequestEvent(body, in)
	if err != nil {
		t.Fatalf("MatchPullRequestEvent: %v", err)
	}
	if matched {
		t.Fatal("want no match once the pull request's current state disagrees with the query")
	}
}

func TestMatchPullRequestEvent_UnlistedRepositoryProducesNoItemAndNoError(t *testing.T) {
	body := pullRequestPayload("acme/other", "https://github.com/acme/other/pull/1", "open", false)
	in := Inputs{Repositories: []string{"acme/widgets"}, State: "open"}
	_, matched, err := MatchPullRequestEvent(body, in)
	if err != nil {
		t.Fatalf("MatchPullRequestEvent: %v", err)
	}
	if matched {
		t.Fatal("want no match for a repository outside the query")
	}
}

func TestMatchPullRequestEvent_MalformedBodyErrors(t *testing.T) {
	_, _, err := MatchPullRequestEvent([]byte("not json"), Inputs{Repositories: []string{"acme/widgets"}, State: "open"})
	if err == nil {
		t.Fatal("want an error for a malformed payload")
	}
}
