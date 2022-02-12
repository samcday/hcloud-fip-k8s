package egress

import (
	"fmt"
	"github.com/vishvananda/netlink"
	"net"
)

func useNAT(pubIface netlink.Link, floatingIP net.IP) error {
	if err := metadataSvcRoute(pubIface); err != nil {
		return fmt.Errorf("error setting metadata route: %w", err)
	}

	if err := unassignFloatingIp(pubIface, floatingIP); err != nil {
		return fmt.Errorf("unassign floating IP failed: %w", err)
	}

	return nil
}

// Remove floating IP from public interface, if assigned.
// `ip addr del <ip> dev <iface>`
func unassignFloatingIp(pubIface netlink.Link, floatingIp net.IP) error {
	addrs, err := netlink.AddrList(pubIface, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if !addr.IPNet.IP.Equal(floatingIp) {
			continue
		}
		_, bits := addr.Mask.Size()
		if bits != 32 {
			continue
		}
		return netlink.AddrDel(pubIface, &addr)
	}
	return nil
}

// Ensure metadata service goes via public interface
// Analogous to `ip route replace 169.254.169.254 scope link`
func metadataSvcRoute(pubIface netlink.Link) error {
	metadataIP, err := netlink.ParseIPNet("169.254.169.254/32")
	if err != nil {
		return err
	}
	return netlink.RouteReplace(&netlink.Route{
		LinkIndex: pubIface.Attrs().Index,
		Dst:       metadataIP,
		Scope:     netlink.SCOPE_LINK,
	})
}
