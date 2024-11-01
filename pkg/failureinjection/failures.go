package failureinjection

type FailureMode interface {
	Setup(Run func()) error
	Attack(Run func()) error
	Restore(Run func()) error
}
