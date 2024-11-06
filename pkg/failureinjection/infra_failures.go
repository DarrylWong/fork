package failureinjection

type NodeRestart struct {
	GracefulRestart    bool
	WaitForReplication bool
	LogFunc            func(f string, args ...interface{})
}

func (f NodeRestart) Setup(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return Run("touch", "NodeRestart.txt")
}
func (f NodeRestart) Attack(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return Run("echo", "NodeRestart Attack", ">>", "NodeRestart.txt")
}
func (f NodeRestart) Restore(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return Run("echo", "NodeRestart Restore", ">>", "NodeRestart.txt")
}
