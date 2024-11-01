package failureinjection

import "fmt"

type LimitBandwidth struct {
	Rate string
}

func (f LimitBandwidth) Setup(Run func()) error {
	fmt.Printf("TODO: implement Setup() %v\n", f)
	return nil
}
func (f LimitBandwidth) Attack(Run func()) error {
	fmt.Printf("TODO: implement Attack() %v\n", f)
	return nil
}
func (f LimitBandwidth) Restore(Run func()) error {
	fmt.Printf("TODO: implement Restore() %v\n", f)
	return nil
}
