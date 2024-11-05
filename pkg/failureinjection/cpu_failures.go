package failureinjection

type PageFault struct {
	MinorFault bool
	MajorFault bool
	LogFunc    func(f string, args ...interface{})
}

func (f PageFault) Setup(Run func()) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return nil
}
func (f PageFault) Attack(Run func()) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return nil
}
func (f PageFault) Restore(Run func()) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return nil
}
