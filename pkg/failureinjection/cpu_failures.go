package failureinjection

import "fmt"

type PageFault struct {
	MinorFault bool
	MajorFault bool
}

func (f PageFault) Setup(Run func()) error {
	fmt.Printf("TODO: implement Setup() %v\n", f)
	return nil
}
func (f PageFault) Attack(Run func()) error {
	fmt.Printf("TODO: implement Attack() %v\n", f)
	return nil
}
func (f PageFault) Restore(Run func()) error {
	fmt.Printf("TODO: implement Restore() %v\n", f)
	return nil
}
