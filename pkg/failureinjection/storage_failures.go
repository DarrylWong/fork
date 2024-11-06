package failureinjection

type DiskStall struct {
	ReadStall  bool
	WriteStall bool
	LogFunc    func(f string, args ...interface{})
}

func (f DiskStall) Setup(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return Run("touch", "DiskStall.txt")
}
func (f DiskStall) Attack(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return Run("echo", "DiskStall Attack", ">>", "DiskStall.txt")
}
func (f DiskStall) Restore(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return Run("echo", "DiskStall Restore", ">>", "DiskStall.txt")
}
