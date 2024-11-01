package failureinjection

import "fmt"

type DiskStall struct {
	ReadStall  bool
	WriteStall bool
}

func (f DiskStall) Setup(Run func()) error {
	fmt.Printf("TODO: implement Setup() %v\n", f)
	return nil
}
func (f DiskStall) Attack(Run func()) error {
	fmt.Printf("TODO: implement Attack() %v\n", f)
	return nil
}
func (f DiskStall) Restore(Run func()) error {
	fmt.Printf("TODO: implement Restore() %v\n", f)
	return nil
}
