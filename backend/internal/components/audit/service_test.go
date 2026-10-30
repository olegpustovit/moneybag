package audit

import (
	"encoding/json"
	"testing"
)

func TestOutcomeDigestBindsIntentOwnerAndData(t *testing.T) {
	o := Outcome{UserID: "owner", ID: "event", Status: "completed", Before: json.RawMessage(`{"v":1}`), After: json.RawMessage(`{"v":2}`)}
	sum := OutcomeDigest("intent", o)
	o.Hash = sum
	if OutcomeDigest("intent", o) != sum {
		t.Fatal("stored digest changes digest")
	}
	o.After = json.RawMessage(`{"v":3}`)
	if OutcomeDigest("intent", o) == sum {
		t.Fatal("outcome tampering undetected")
	}
	if OutcomeDigest("other-intent", o) == sum {
		t.Fatal("intent not bound")
	}
}

func TestDigestBindsSharedGrant(t *testing.T) {
	event := Event{UserID: "owner", ID: "event"}
	withoutGrant := Digest(event)
	event.GrantID = "grant-one"
	withGrant := Digest(event)
	if withGrant == withoutGrant {
		t.Fatal("grant missing from digest")
	}
	event.GrantID = "grant-two"
	if Digest(event) == withGrant {
		t.Fatal("grant change did not change digest")
	}
	withGrant = Digest(event)
	event.GrantHash = "permissions"
	if Digest(event) == withGrant {
		t.Fatal("grant permissions missing from digest")
	}
}
