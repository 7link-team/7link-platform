package control

import "errors"

type runtimeProcessControl interface {
	SetPaused(bool) (bool, error)
	IsPaused() bool
}

func (j *journal) acknowledgeProcessControl(store *Store, ack receiptAck, cancelled bool) error {
	if cancelled || j.Attempt == nil || j.control == nil || (!ack.Pause && !ack.Resume) {
		return nil
	}
	if ack.Pause && ack.Resume {
		return errors.New("conflicting runtime pause/resume requests")
	}
	changed, err := j.control.SetPaused(ack.Pause)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	kind := "RESUMED"
	if ack.Pause {
		kind = "PAUSED"
	}
	return j.enqueue(store, receipt{Kind: kind})
}
