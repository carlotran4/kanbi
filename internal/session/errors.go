package session

import (
	"errors"

	"kanbi/internal/storage"
)

var ErrPromptAlreadySent = errors.New("prompt already sent; open session instead")

type RepairNeededError struct {
	Ticket storage.Ticket
	Reason string
}

func (e RepairNeededError) Error() string {
	if e.Reason == "" {
		return "ticket session needs repair"
	}
	return e.Reason
}

type ResumeFailedError struct {
	Ticket storage.Ticket
	Err    error
}

func (e ResumeFailedError) Error() string {
	if e.Err == nil {
		return "resume failed"
	}
	return "resume failed: " + e.Err.Error()
}

func (e ResumeFailedError) Unwrap() error { return e.Err }

type PromptReadyError struct {
	WindowName string
	Prompt     string
	Ready      string
	Err        error
}

func (e PromptReadyError) Error() string {
	if e.Err == nil {
		return "prompt readiness timeout"
	}
	return e.Err.Error()
}

func (e PromptReadyError) Unwrap() error { return e.Err }
