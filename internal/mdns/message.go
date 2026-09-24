package mdns

import (
	"net"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	// hostTTL is the RFC 6762 recommendation for address records.
	hostTTL = 120
	// legacyTTL caps answers to one-shot resolvers that did not use port 5353.
	legacyTTL = 10

	cacheFlush   = 0x8000
	unicastReply = 0x8000
	typeNSEC     = dnsmessage.Type(47)
)

// query is the part of an incoming mDNS packet the responder acts on.
type query struct {
	id        uint16
	response  bool
	questions []dnsmessage.Question
	answers   []dnsmessage.Resource
}

func parse(packet []byte) (query, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		return query{}, err
	}
	questions, err := parser.AllQuestions()
	if err != nil {
		return query{}, err
	}
	answers, err := parser.AllAnswers()
	if err != nil {
		// Truncated or unusual answer sections still carry usable questions.
		answers = nil
	}
	return query{id: header.ID, response: header.Response, questions: questions, answers: answers}, nil
}

// canonical lowercases a DNS name and ensures a trailing dot.
func canonical(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return name
}

// reply is the answer for one query, or nil when nothing we own was asked.
// legacy is true when the querier did not send from port 5353 (RFC 6762 §6.7):
// the reply then echoes the id and questions, omits cache-flush, and uses a short TTL.
func reply(q query, owns func(string) bool, addr net.IP, legacy bool) *dnsmessage.Message {
	ip := addr.To4()
	if q.response || ip == nil {
		return nil
	}
	msg := &dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}}
	if legacy {
		msg.Header.ID = q.id
	}
	seen := map[string]bool{}
	for _, question := range q.questions {
		name := canonical(question.Name.String())
		if !owns(name) || seen[name+question.Type.String()] {
			continue
		}
		seen[name+question.Type.String()] = true
		switch question.Type {
		case dnsmessage.TypeA, dnsmessage.TypeALL:
			if knownAnswer(q.answers, name, ip) {
				continue
			}
			msg.Answers = append(msg.Answers, addressRecord(question.Name, ip, legacy, hostTTL))
			msg.Additionals = append(msg.Additionals, nsecRecord(question.Name, legacy))
		case dnsmessage.TypeAAAA:
			// No IPv6 record: say so, so resolvers stop waiting for one.
			msg.Answers = append(msg.Answers, nsecRecord(question.Name, legacy))
		default:
			continue
		}
		if legacy {
			msg.Questions = append(msg.Questions, dnsmessage.Question{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET})
		}
	}
	if len(msg.Answers) == 0 {
		return nil
	}
	return msg
}

// wantsUnicast reports whether any question set the QU bit.
func wantsUnicast(q query) bool {
	for _, question := range q.questions {
		if question.Class&unicastReply != 0 {
			return true
		}
	}
	return false
}

// knownAnswer implements known-answer suppression: skip a record the querier
// already holds with more than half its TTL left.
func knownAnswer(answers []dnsmessage.Resource, name string, ip net.IP) bool {
	for _, answer := range answers {
		if answer.Header.Type != dnsmessage.TypeA || canonical(answer.Header.Name.String()) != name {
			continue
		}
		record, ok := answer.Body.(*dnsmessage.AResource)
		if ok && net.IP(record.A[:]).Equal(ip) && answer.Header.TTL > hostTTL/2 {
			return true
		}
	}
	return false
}

func addressRecord(name dnsmessage.Name, ip net.IP, legacy bool, ttl uint32) dnsmessage.Resource {
	class := dnsmessage.ClassINET | cacheFlush
	if legacy {
		class = dnsmessage.ClassINET
		ttl = min(ttl, legacyTTL)
	}
	var a [4]byte
	copy(a[:], ip.To4())
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeA, Class: class, TTL: ttl},
		Body:   &dnsmessage.AResource{A: a},
	}
}

// nsecRecord states that name has an A record and nothing else (RFC 6762 §6.1).
func nsecRecord(name dnsmessage.Name, legacy bool) dnsmessage.Resource {
	class := dnsmessage.ClassINET | cacheFlush
	ttl := uint32(hostTTL)
	if legacy {
		class = dnsmessage.ClassINET
		ttl = legacyTTL
	}
	data := append(encodeName(name.String()), 0, 1, 0x40) // window 0, 1 byte, bit for type A
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: name, Type: typeNSEC, Class: class, TTL: ttl},
		Body:   &dnsmessage.UnknownResource{Type: typeNSEC, Data: data},
	}
}

func encodeName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

// announcement is an unsolicited response for name at ip; ttl 0 is a goodbye.
func announcement(name string, ip net.IP, ttl uint32) ([]byte, error) {
	dnsName, err := dnsmessage.NewName(canonical(name))
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{
		Header:  dnsmessage.Header{Response: true, Authoritative: true},
		Answers: []dnsmessage.Resource{addressRecord(dnsName, ip, false, ttl)},
	}
	return msg.Pack()
}

// probe asks whether anyone else already answers for name (RFC 6762 §8.1).
func probe(name string, ip net.IP) ([]byte, error) {
	dnsName, err := dnsmessage.NewName(canonical(name))
	if err != nil {
		return nil, err
	}
	proposed := addressRecord(dnsName, ip, false, hostTTL)
	proposed.Header.Class = dnsmessage.ClassINET
	msg := dnsmessage.Message{
		Questions:   []dnsmessage.Question{{Name: dnsName, Type: dnsmessage.TypeALL, Class: dnsmessage.ClassINET | unicastReply}},
		Authorities: []dnsmessage.Resource{proposed},
	}
	return msg.Pack()
}

// claims returns the names another host's response asserts an address for.
func claims(q query) map[string]net.IP {
	out := map[string]net.IP{}
	if !q.response {
		return out
	}
	for _, answer := range q.answers {
		record, ok := answer.Body.(*dnsmessage.AResource)
		if !ok || answer.Header.TTL == 0 {
			continue
		}
		out[canonical(answer.Header.Name.String())] = net.IP(append([]byte(nil), record.A[:]...))
	}
	return out
}
