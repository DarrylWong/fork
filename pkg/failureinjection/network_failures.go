package failureinjection

type LimitBandwidth struct {
	Rate    string
	LogFunc func(f string, args ...interface{})
}

func (f LimitBandwidth) Setup(Run func()) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return nil
}
func (f LimitBandwidth) Attack(Run func()) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return nil
}
func (f LimitBandwidth) Restore(Run func()) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return nil
}
