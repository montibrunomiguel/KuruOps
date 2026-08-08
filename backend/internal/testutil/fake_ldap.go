package testutil

// FakeLDAPServer is a minimal, in-process fake LDAP directory, speaking just
// enough of the wire protocol (BindRequest/BindResponse, SearchRequest/
// SearchResultEntry/SearchResultDone) for authn.AuthenticateLDAP's two-bind
// pattern -- built directly on go-asn1-ber (already a transitive dependency
// of go-ldap/ldap/v3) rather than pulling in a third-party test-server
// package, so the test surface stays limited to a library already vetted by
// govulncheck. See RFC 4511 for the message shapes this mirrors.
//
// Shared across internal/authn (direct AuthenticateLDAP tests) and
// internal/service (LDAPAuthService.Login's real bind path) so both stop
// depending on a live directory -- this replaced relying on the public
// FreeIPA demo server used to live-verify LDAP earlier in development, which
// proved flaky (an account got server-side deactivated mid-session), which
// is exactly the kind of external dependency a unit test suite shouldn't
// have.

import (
	"net"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/stretchr/testify/require"
)

const (
	LDAPResultSuccess            = 0
	LDAPResultOperationsError    = 1
	LDAPResultInvalidCredentials = 49
)

type FakeLDAPUser struct {
	DN          string
	Mail        string
	Password    string
	CN          string
	DisplayName string
	Groups      []string
}

// FakeLDAPServer's zero value is a usable server with no users -- every
// bind fails and every search finds nothing, which is itself a useful
// starting point for some test cases.
type FakeLDAPServer struct {
	BindDN       string
	BindPassword string
	GroupAttr    string
	Users        []FakeLDAPUser

	// SearchResultCode, when non-zero, makes every SearchRequest respond
	// with this LDAP result code and no entries, instead of matching
	// against Users -- simulates a directory-side search failure.
	SearchResultCode int64
}

// StartFakeLDAPServer starts srv listening on an ephemeral localhost port,
// stopping it via t.Cleanup.
func StartFakeLDAPServer(t *testing.T, srv *FakeLDAPServer) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed by t.Cleanup
			}
			go srv.handleConn(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (s *FakeLDAPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	for {
		pkt, err := ber.ReadPacket(conn)
		if err != nil {
			return // client closed the connection (or sent Unbind then closed)
		}
		if len(pkt.Children) < 2 {
			return
		}
		messageID, _ := pkt.Children[0].Value.(int64)
		op := pkt.Children[1]

		switch op.Tag {
		case 0: // BindRequest
			s.handleBind(conn, messageID, op)
		case 3: // SearchRequest
			s.handleSearch(conn, messageID, op)
		default: // UnbindRequest or anything else this fake doesn't speak
			return
		}
	}
}

func (s *FakeLDAPServer) handleBind(conn net.Conn, messageID int64, op *ber.Packet) {
	if len(op.Children) < 3 {
		_ = writeLDAPMessage(conn, messageID, ldapResultMessage(1, LDAPResultOperationsError))
		return
	}
	dn, _ := op.Children[1].Value.(string)
	password := op.Children[2].Data.String() // ClassContext primitive: never gets .Value set, see readPacket

	ok := dn == s.BindDN && password == s.BindPassword
	for _, u := range s.Users {
		if u.DN == dn && u.Password == password {
			ok = true
		}
	}

	code := int64(LDAPResultSuccess)
	if !ok {
		code = LDAPResultInvalidCredentials
	}
	_ = writeLDAPMessage(conn, messageID, ldapResultMessage(1, code))
}

func (s *FakeLDAPServer) handleSearch(conn net.Conn, messageID int64, op *ber.Packet) {
	if s.SearchResultCode != 0 {
		_ = writeLDAPMessage(conn, messageID, ldapResultMessage(5, s.SearchResultCode))
		return
	}
	if len(op.Children) < 7 {
		_ = writeLDAPMessage(conn, messageID, ldapResultMessage(5, LDAPResultOperationsError))
		return
	}

	filterPkt := op.Children[6]
	var attrValue string
	if len(filterPkt.Children) >= 2 {
		attrValue, _ = filterPkt.Children[1].Value.(string)
	}

	for _, u := range s.Users {
		if u.Mail == attrValue {
			_ = writeLDAPMessage(conn, messageID, s.buildSearchResultEntry(u))
		}
	}
	_ = writeLDAPMessage(conn, messageID, ldapResultMessage(5, LDAPResultSuccess))
}

func (s *FakeLDAPServer) buildSearchResultEntry(u FakeLDAPUser) *ber.Packet {
	entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(4), nil, "SearchResultEntry")
	entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, u.DN, "objectName"))

	attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")
	addAttr := func(name string, values []string) {
		if len(values) == 0 {
			return
		}
		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "type"))
		vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "vals")
		for _, v := range values {
			vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, v, "val"))
		}
		a.AppendChild(vals)
		attrs.AppendChild(a)
	}
	addAttr(s.GroupAttr, u.Groups)
	if u.CN != "" {
		addAttr("cn", []string{u.CN})
	}
	if u.DisplayName != "" {
		addAttr("displayName", []string{u.DisplayName})
	}
	entry.AppendChild(attrs)
	return entry
}

// ldapResultMessage builds an LDAPResult (RFC 4511 4.1.9) tagged with the
// given [APPLICATION n]: 1 for BindResponse, 5 for SearchResultDone. Both
// share the same resultCode/matchedDN/diagnosticMessage shape.
func ldapResultMessage(appTag ber.Tag, code int64) *ber.Packet {
	pkt := ber.Encode(ber.ClassApplication, ber.TypeConstructed, appTag, nil, "")
	pkt.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	pkt.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	pkt.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	return pkt
}

func writeLDAPMessage(conn net.Conn, messageID int64, op *ber.Packet) error {
	envelope := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAPMessage")
	envelope.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, messageID, "messageID"))
	envelope.AppendChild(op)
	_, err := conn.Write(envelope.Bytes())
	return err
}
