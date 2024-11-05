package failureinjection

type NodeRestart struct {
	GracefulRestart    bool
	WaitForReplication bool
	LogFunc            func(f string, args ...interface{})
}

func (f NodeRestart) Setup(Run func()) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return nil
}
func (f NodeRestart) Attack(Run func()) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return nil
}
func (f NodeRestart) Restore(Run func()) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return nil
}
