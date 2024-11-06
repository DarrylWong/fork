package failureinjection

type FailureMode interface {
	Setup(Run func(args ...string) error) error
	Attack(Run func(args ...string) error) error
	Restore(Run func(args ...string) error) error
}
