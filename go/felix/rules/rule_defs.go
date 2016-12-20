// Copyright (c) 2016 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rules

import (
	log "github.com/Sirupsen/logrus"
	"github.com/projectcalico/felix/go/felix/ipsets"
	"github.com/projectcalico/felix/go/felix/iptables"
	"github.com/projectcalico/felix/go/felix/proto"
	"net"
	"strings"
)

const (
	ChainNamePrefix = "cali"
	IPSetNamePrefix = "cali"

	ChainFilterInput   = ChainNamePrefix + "-INPUT"
	ChainFilterForward = ChainNamePrefix + "-FORWARD"
	ChainFilterOutput  = ChainNamePrefix + "-OUTPUT"

	ChainNATPrerouting  = ChainNamePrefix + "-PREROUTING"
	ChainNATPostrouting = ChainNamePrefix + "-POSTROUTING"
	ChainNATOutgoing    = ChainNamePrefix + "-nat-outgoing"

	IPSetIDNATOutgoingAllPools  = "all-ipam-pools"
	IPSetIDNATOutgoingMasqPools = "masq-ipam-pools"

	IPSetIDAllHostIPs = "all-hosts"

	PolicyInboundPfx  = ChainNamePrefix + "pi-"
	PolicyOutboundPfx = ChainNamePrefix + "po-"

	ChainWorkloadToHost       = ChainNamePrefix + "-wl-to-host"
	ChainFromWorkloadDispatch = ChainNamePrefix + "-from-wl-dispatch"
	ChainToWorkloadDispatch   = ChainNamePrefix + "-to-wl-dispatch"

	ChainDispatchToHostEndpoint   = ChainNamePrefix + "-to-host-endpoint"
	ChainDispatchFromHostEndpoint = ChainNamePrefix + "-from-host-endpoint"

	WorkloadToEndpointPfx   = ChainNamePrefix + "tw-"
	WorkloadFromEndpointPfx = ChainNamePrefix + "fw-"

	HostToEndpointPfx   = ChainNamePrefix + "th-"
	HostFromEndpointPfx = ChainNamePrefix + "fh-"

	RuleHashPrefix = "cali:"

	// HistoricNATRuleInsertRegex is a regex pattern to match to match
	// special-case rules inserted by old versions of felix.  Specifically,
	// Python felix used to insert a masquerade rule directly into the
	// POSTROUTING chain.
	//
	// Note: this regex depends on the output format of iptables-save so,
	// where possible, it's best to match only on part of the rule that
	// we're sure can't change (such as the ipset name in the masquerade
	// rule).
	HistoricInsertedNATRuleRegex = `-A POSTROUTING .* felix-masq-ipam-pools .*|` +
		`-A POSTROUTING -o tunl0 -m addrtype ! --src-type LOCAL --limit-iface-out -m addrtype --src-type LOCAL -j MASQUERADE`
)

var (
	// AllHistoricChainNamePrefixes lists all the prefixes that we've used for chains.  Keeping
	// track of the old names lets us clean them up.
	AllHistoricChainNamePrefixes = []string{"felix-", "cali"}
	// AllHistoricIPSetNamePrefixes, similarly contains all the prefixes we've ever used for IP
	// sets.
	AllHistoricIPSetNamePrefixes = []string{"felix-", "cali"}
	// LegacyV4IPSetNames contains some extra IP set names that were used in older versions of
	// Felix and don't fit our versioned pattern.
	LegacyV4IPSetNames = []string{"felix-masq-ipam-pools", "felix-all-ipam-pools"}
)

type RuleRenderer interface {
	StaticFilterTableChains(ipVersion uint8) []*iptables.Chain
	StaticNATTableChains(ipVersion uint8) []*iptables.Chain

	WorkloadDispatchChains(map[proto.WorkloadEndpointID]*proto.WorkloadEndpoint) []*iptables.Chain
	WorkloadEndpointToIptablesChains(epID *proto.WorkloadEndpointID, endpoint *proto.WorkloadEndpoint) []*iptables.Chain

	HostDispatchChains(map[string]*proto.HostEndpointID) []*iptables.Chain
	HostEndpointToIptablesChains(epID *proto.HostEndpointID, endpoint *proto.HostEndpoint) []*iptables.Chain

	PolicyToIptablesChains(policyID *proto.PolicyID, policy *proto.Policy, ipVersion uint8) []*iptables.Chain
	ProfileToIptablesChains(policyID *proto.ProfileID, policy *proto.Profile, ipVersion uint8) []*iptables.Chain
	ProtoRuleToIptablesRules(pRule *proto.Rule, ipVersion uint8) []iptables.Rule

	NATOutgoingChain(active bool, ipVersion uint8) *iptables.Chain
}

type ruleRenderer struct {
	Config

	dropActions        []iptables.Action
	inputAcceptActions []iptables.Action
}

func (r *ruleRenderer) ipSetConfig(ipVersion uint8) *ipsets.IPVersionConfig {
	if ipVersion == 4 {
		return r.IPSetConfigV4
	} else if ipVersion == 6 {
		return r.IPSetConfigV6
	} else {
		log.WithField("version", ipVersion).Panic("Unknown IP version")
		return nil
	}
}

type Config struct {
	IPSetConfigV4 *ipsets.IPVersionConfig
	IPSetConfigV6 *ipsets.IPVersionConfig

	WorkloadIfacePrefixes []string

	IptablesMarkAccept   uint32
	IptablesMarkNextTier uint32

	OpenStackMetadataIP          net.IP
	OpenStackMetadataPort        uint16
	OpenStackSpecialCasesEnabled bool

	IPIPEnabled       bool
	IPIPTunnelAddress net.IP

	ActionOnDrop         string
	EndpointToHostAction string
}

func NewRenderer(config Config) RuleRenderer {
	// Convert configured actions to rule slices.  First, what should we actually do when we'd
	// normally drop a packet?  For sandbox mode, we support allowing the packet instead, or
	// logging it.
	var dropActions []iptables.Action
	if strings.HasPrefix(config.ActionOnDrop, "LOG-") {
		log.Warn("Action on drop includes LOG.  All dropped packets will be logged.")
		dropActions = append(dropActions, iptables.LogAction{Prefix: "calico-drop"})
	}
	if strings.HasSuffix(config.ActionOnDrop, "ACCEPT") {
		log.Warn("Action on drop set to ACCEPT.  Calico security is disabled!")
		dropActions = append(dropActions, iptables.AcceptAction{})
	} else {
		dropActions = append(dropActions, iptables.DropAction{})
	}

	// Second, what should we do with packets that come from workloads to the host itself.
	var inputAcceptActions []iptables.Action
	switch config.EndpointToHostAction {
	case "DROP":
		log.Info("Workload to host packets will be dropped.")
		inputAcceptActions = dropActions
	case "ACCEPT":
		log.Info("Workload to host packets will be accepted.")
		inputAcceptActions = []iptables.Action{iptables.AcceptAction{}}
	default:
		log.Info("Workload to host packets will be returned to INPUT chain.")
		inputAcceptActions = []iptables.Action{iptables.ReturnAction{}}
	}

	return &ruleRenderer{
		Config:             config,
		dropActions:        dropActions,
		inputAcceptActions: inputAcceptActions,
	}
}
