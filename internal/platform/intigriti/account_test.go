package intigriti

import (
	"encoding/json"
	"testing"
)

// The shapes below are the live answers read from a logged-in session on
// 2026-11-20: /api/user/user, and the researcher API's /v1/payouts. If the
// platform changes a field name these fixtures stop parsing and say so.

const accountJSON = `{
  "reputation": 4,
  "rank": 9643,
  "identityChecked": 4,
  "identityCheckAttempts": 1,
  "sanctionCleared": true,
  "country": "CN",
  "city": "Shanghai",
  "skills": ["mobile_hacking", "api", "web_hacking"],
  "profileCompleted": true,
  "userName": "n1gh7glovv",
  "userId": "ee1efede-78f8-42e3-9195-9912dcc951a8",
  "roles": ["RESEARCHER"]
}`

func TestAccountParsesTheLiveAnswer(t *testing.T) {
	var a Account
	if err := json.Unmarshal([]byte(accountJSON), &a); err != nil {
		t.Fatal(err)
	}
	if a.UserName != "n1gh7glovv" || a.Reputation != 4 || a.Rank != 9643 {
		t.Fatalf("read %+v", a)
	}
	if !a.Verified() {
		t.Error("an identity state above zero reads as not verified")
	}
	if len(a.Skills) != 3 || a.Country != "CN" {
		t.Fatalf("skills or country lost: %+v", a)
	}
}

// A fresh account has nothing pending, and the platform answers with an empty
// array rather than null: parsing must not treat that as an error.
func TestInvitesAndApplicationsParseEmpty(t *testing.T) {
	var inv []Invite
	if err := json.Unmarshal([]byte("[]"), &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv) != 0 {
		t.Fatalf("%d invites from an empty answer", len(inv))
	}
	var apps []Application
	if err := json.Unmarshal([]byte("[]"), &apps); err != nil {
		t.Fatal(err)
	}
}

// The BETA payouts envelope: a maxCount and a records array, with the amount as
// a money object rather than a number.
const payoutsJSON = `{
  "maxCount": 1,
  "records": [{
    "id": "p1",
    "amount": {"value": 250.5, "currency": "EUR"},
    "status": {"id": 3, "value": "Paid"},
    "createdAt": "2026-10-01T10:00:00Z",
    "paidAt": "2026-10-15T10:00:00Z"
  }]
}`

func TestPayoutsParseTheBetaEnvelope(t *testing.T) {
	var page struct {
		MaxCount int      "json:\"maxCount\""
		Records  []Payout "json:\"records\""
	}
	if err := json.Unmarshal([]byte(payoutsJSON), &page); err != nil {
		t.Fatal(err)
	}
	if page.MaxCount != 1 || len(page.Records) != 1 {
		t.Fatalf("envelope read as %+v", page)
	}
	if page.Records[0].Amount.Value != 250.5 || page.Records[0].Amount.Currency != "EUR" {
		t.Fatalf("amount read as %+v", page.Records[0].Amount)
	}
	if page.Records[0].Status.Value != "Paid" {
		t.Fatalf("status read as %+v", page.Records[0].Status)
	}
}
