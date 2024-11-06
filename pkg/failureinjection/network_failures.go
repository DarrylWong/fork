package failureinjection

type LimitBandwidth struct {
	Rate    string
	LogFunc func(f string, args ...interface{})
}

func (f LimitBandwidth) Setup(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Setup() %v\n", f)
	return Run("touch", "LimitBandwidth.txt")
}
func (f LimitBandwidth) Attack(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Attack() %v\n", f)
	return Run("echo", "LimitBandwidth Attack", ">>", "LimitBandwidth.txt")
}
func (f LimitBandwidth) Restore(Run func(args ...string) error) error {
	f.LogFunc("TODO: implement Restore() %v\n", f)
	return Run("echo", "LimitBandwidth Restore", ">>", "LimitBandwidth.txt")
}
