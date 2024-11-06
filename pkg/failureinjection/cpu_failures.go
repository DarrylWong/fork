package failureinjection

type PageFault struct {
	MinorFault bool
	MajorFault bool
	LogFunc    func(f string, args ...interface{})
}

func (f PageFault) Setup(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Setup() %+v", f)
	return Run("touch", "PageFault.txt")
}
func (f PageFault) Attack(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Attack() %+v", f)
	return Run("echo", "Page Fault Attack", ">>", "PageFault.txt")
}
func (f PageFault) Restore(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Restore() %+v", f)
	return Run("echo", "Page Fault Restore", ">>", "PageFault.txt")
}
