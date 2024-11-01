package failureinjection

import "fmt"

type NodeRestart struct {
	GracefulRestart    bool
	WaitForReplication bool
}

func (f NodeRestart) Setup(Run func()) error {
	fmt.Printf("TODO: implement Setup() %v\n", f)
	return nil
}
func (f NodeRestart) Attack(Run func()) error {
	fmt.Printf("TODO: implement Attack() %v\n", f)
	return nil
}
func (f NodeRestart) Restore(Run func()) error {
	fmt.Printf("TODO: implement Restore() %v\n", f)
	return nil
}
