package failureinjection

import "fmt"

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

type PartitionNode struct {
	Port    int
	LogFunc func(f string, args ...interface{})
}

func (f PartitionNode) Setup(Run func(args ...string) error) error {
	// ip tables should already be installed.
	return nil
}
func (f PartitionNode) Attack(Run func(args ...string) error) error {
	cmd := fmt.Sprintf(`
# ensure any failure fails the entire script.
set -e;

# Setting default filter policy
sudo iptables -P INPUT ACCEPT;
sudo iptables -P OUTPUT ACCEPT;

# Drop any node-to-node crdb traffic.
sudo iptables -A INPUT -p tcp --dport %[1]d -j DROP;
sudo iptables -A OUTPUT -p tcp --dport %[1]d -j DROP;

sudo iptables-save
`, f.Port)
	return Run(cmd)
}
func (f PartitionNode) Restore(Run func(args ...string) error) error {
	cmd := fmt.Sprintf(`
set -e;
sudo iptables -D INPUT -p tcp --dport %[1]d -j DROP;
sudo iptables -D OUTPUT -p tcp --dport %[1]d -j DROP;
sudo iptables-save
`, f.Port)
	return Run(cmd)
}
