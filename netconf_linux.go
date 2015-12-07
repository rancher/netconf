package netconf

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"

	log "github.com/Sirupsen/logrus"

	"github.com/ryanuber/go-glob"
	"github.com/vishvananda/netlink"
)

const (
	CONF       = "/var/lib/rancher/conf"
	NET_SCRIPT = "/var/lib/rancher/conf/network.sh"
)

func createInterfaces(netCfg *NetworkConfig) error {
	for name, iface := range netCfg.Interfaces {
		if !iface.Bridge {
			continue
		}

		bridge := netlink.Bridge{}
		bridge.LinkAttrs.Name = name

		if err := netlink.LinkAdd(&bridge); err != nil {
			log.Errorf("Failed to create bridge %s: %v", name, err)
		}
	}

	return nil
}

func runScript(netCfg *NetworkConfig) error {
	if netCfg.Script == "" {
		return nil
	}

	if _, err := os.Stat(CONF); os.IsNotExist(err) {
		if err := os.MkdirAll(CONF, 0700); err != nil {
			return err
		}
	}

	if err := ioutil.WriteFile(NET_SCRIPT, []byte(netCfg.Script), 0700); err != nil {
		return err
	}

	cmd := exec.Command(NET_SCRIPT)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ApplyNetworkConfigs(netCfg *NetworkConfig) error {
	log.Debugf("Config: %#v", netCfg)
	if err := runScript(netCfg); err != nil {
		log.Errorf("Failed to run script: %v", err)
	}

	if err := createInterfaces(netCfg); err != nil {
		return err
	}

	links, err := netlink.LinkList()
	if err != nil {
		return err
	}

	dhcpLinks := []string{}

	//apply network config
	for _, link := range links {
		linkName := link.Attrs().Name
		var match InterfaceConfig

		for key, netConf := range netCfg.Interfaces {
			if netConf.Match == "" {
				netConf.Match = key
			}

			if netConf.Match == "" {
				continue
			}

			if len(netConf.Match) > 4 && strings.ToLower(netConf.Match[:3]) == "mac" {
				haAddr, err := net.ParseMAC(netConf.Match[4:])
				if err != nil {
					return err
				}
				if bytes.Compare(haAddr, link.Attrs().HardwareAddr) == 0 {
					// MAC address match is used over all other matches
					match = netConf
					break
				}
			}

			// "" means match has not been found
			if match.Match == "" && matches(linkName, netConf.Match) {
				match = netConf
			}

			if netConf.Match == linkName {
				// Found exact match, use it over wildcard match
				match = netConf
			}
		}

		if match.Match != "" {
			if match.DHCP {
				dhcpLinks = append(dhcpLinks, link.Attrs().Name)
			} else if err = applyNetConf(link, match); err != nil {
				log.Errorf("Failed to apply settings to %s : %v", linkName, err)
			}
		}
	}

	if len(dhcpLinks) > 0 {
		log.Infof("Running DHCP on %v", dhcpLinks)
		dhcpcdArgs := append([]string{"-MA4", "-e", "force_hostname=true"}, dhcpLinks...)
		cmd := exec.Command("dhcpcd", dhcpcdArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Error(err)
		}
	}

	if err != nil {
		return err
	}

	return nil
}

func applyNetConf(link netlink.Link, netConf InterfaceConfig) error {
	if netConf.IPV4LL {
		if err := AssignLinkLocalIP(link); err != nil {
			log.Errorf("IPV4LL set failed: %v", err)
			return err
		}
	} else if netConf.Address == "" {
		return nil
	} else {
		addr, err := netlink.ParseAddr(netConf.Address)
		if err != nil {
			return err
		}
		if err := netlink.AddrAdd(link, addr); err != nil {
			//Ignore this error
			log.Errorf("addr add failed: %v", err)
		} else {
			log.Infof("Set %s on %s", netConf.Address, link.Attrs().Name)
		}
	}

	if netConf.MTU > 0 {
		if err := netlink.LinkSetMTU(link, netConf.MTU); err != nil {
			log.Errorf("set MTU Failed: %v", err)
			return err
		}
	}

	if err := netlink.LinkSetUp(link); err != nil {
		log.Errorf("failed to setup link: %v", err)
		return err
	}

	if netConf.Gateway != "" {
		gatewayIp := net.ParseIP(netConf.Gateway)
		if gatewayIp == nil {
			return errors.New("Invalid gateway address " + netConf.Gateway)
		}

		route := netlink.Route{
			Scope: netlink.SCOPE_UNIVERSE,
			Gw:    net.ParseIP(netConf.Gateway),
		}
		if err := netlink.RouteAdd(&route); err != nil {
			log.Errorf("gateway set failed: %v", err)
			return err
		}

		log.Infof("Set default gateway %s", netConf.Gateway)
	}

	return nil
}

func matches(link, conf string) bool {
	return glob.Glob(conf, link)
}
