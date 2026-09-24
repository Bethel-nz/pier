package mdns

import (
	"errors"
	"testing"
	"time"
)

func TestSendErrorsAreReportedUntilASendSucceeds(t *testing.T) {
	r := &Responder{}
	if r.SendError() != nil {
		t.Fatal("new responder reports an error")
	}
	r.noteSend(errors.New("sendto: no route to host"))
	if r.SendError() == nil {
		t.Fatal("failed send not reported")
	}
	r.noteSend(nil)
	if r.SendError() != nil {
		t.Fatal("a successful send should clear the error")
	}
	r.noteSend(errors.New("old failure"))
	r.sendErrAt = time.Now().Add(-2 * time.Minute)
	if r.SendError() != nil {
		t.Fatal("an error older than a minute should not be reported")
	}
}
