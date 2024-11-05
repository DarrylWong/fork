package failureinjection

type DiskStall struct {
	ReadStall  bool
	WriteStall bool
	LogFunc    func(f string, args ...interface{})
}

func (f DiskStall) Setup(Run func()) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return nil
}
func (f DiskStall) Attack(Run func()) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return nil
}
func (f DiskStall) Restore(Run func()) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return nil
}
