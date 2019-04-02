package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/bits"
	"runtime"
	"time"

	u "github.com/ipfs/go-ipfs-util"
	ci "github.com/libp2p/go-libp2p-crypto"
	kb "github.com/libp2p/go-libp2p-kbucket"
	peer "github.com/libp2p/go-libp2p-peer"
)

var (
	difficulty = 18
)

type eclipseKey struct {
	priv ci.PrivKey
	pub  ci.PubKey
}

func Init(out io.Writer, nBitsForKeypair int) (*Config, error) {
	identity, err := identityConfig(out, nBitsForKeypair)
	if err != nil {
		return nil, err
	}

	bootstrapPeers, err := DefaultBootstrapPeers()
	if err != nil {
		return nil, err
	}

	datastore := DefaultDatastoreConfig()

	conf := &Config{
		API: API{
			HTTPHeaders: map[string][]string{},
		},

		// setup the node's default addresses.
		// NOTE: two swarm listen addrs, one tcp, one utp.
		Addresses: addressesConfig(),

		Datastore: datastore,
		Bootstrap: BootstrapPeerStrings(bootstrapPeers),
		Identity:  identity,
		Discovery: Discovery{
			MDNS: MDNS{
				Enabled:  true,
				Interval: 10,
			},
		},

		Routing: Routing{
			Type: "dht",
		},

		// setup the node mount points.
		Mounts: Mounts{
			IPFS: "/ipfs",
			IPNS: "/ipns",
		},

		Ipns: Ipns{
			ResolveCacheSize: 128,
		},

		Gateway: Gateway{
			RootRedirect: "",
			Writable:     false,
			NoFetch:      false,
			PathPrefixes: []string{},
			HTTPHeaders: map[string][]string{
				"Access-Control-Allow-Origin":  []string{"*"},
				"Access-Control-Allow-Methods": []string{"GET"},
				"Access-Control-Allow-Headers": []string{"X-Requested-With", "Range", "User-Agent"},
			},
			APICommands: []string{},
		},
		Reprovider: Reprovider{
			Interval: "12h",
			Strategy: "all",
		},
		Swarm: SwarmConfig{
			ConnMgr: ConnMgr{
				LowWater:    DefaultConnMgrLowWater,
				HighWater:   DefaultConnMgrHighWater,
				GracePeriod: DefaultConnMgrGracePeriod.String(),
				Type:        "basic",
			},
		},
	}

	return conf, nil
}

// DefaultConnMgrHighWater is the default value for the connection managers
// 'high water' mark
const DefaultConnMgrHighWater = 900

// DefaultConnMgrLowWater is the default value for the connection managers 'low
// water' mark
const DefaultConnMgrLowWater = 600

// DefaultConnMgrGracePeriod is the default value for the connection managers
// grace period
const DefaultConnMgrGracePeriod = time.Second * 20

func addressesConfig() Addresses {
	return Addresses{
		Swarm: []string{
			"/ip4/0.0.0.0/tcp/4001",
			// "/ip4/0.0.0.0/udp/4002/utp", // disabled for now.
			"/ip6/::/tcp/4001",
		},
		Announce:   []string{},
		NoAnnounce: []string{},
		API:        Strings{"/ip4/127.0.0.1/tcp/5001"},
		Gateway:    Strings{"/ip4/127.0.0.1/tcp/8080"},
	}
}

// DefaultDatastoreConfig is an internal function exported to aid in testing.
func DefaultDatastoreConfig() Datastore {
	return Datastore{
		StorageMax:         "10GB",
		StorageGCWatermark: 90, // 90%
		GCPeriod:           "1h",
		BloomFilterSize:    0,
		Spec: map[string]interface{}{
			"type": "mount",
			"mounts": []interface{}{
				map[string]interface{}{
					"mountpoint": "/blocks",
					"type":       "measure",
					"prefix":     "flatfs.datastore",
					"child": map[string]interface{}{
						"type":      "flatfs",
						"path":      "blocks",
						"sync":      true,
						"shardFunc": "/repo/flatfs/shard/v1/next-to-last/2",
					},
				},
				map[string]interface{}{
					"mountpoint": "/",
					"type":       "measure",
					"prefix":     "leveldb.datastore",
					"child": map[string]interface{}{
						"type":        "levelds",
						"path":        "datastore",
						"compression": "none",
					},
				},
			},
		},
	}
}

// identityConfig initializes a new identity.
func identityConfig(out io.Writer, nbits int) (Identity, error) {
	// TODO guard higher up
	ident := Identity{}
	if nbits < 1024 {
		return ident, errors.New("bitsize less than 1024 is considered unsafe")
	}

	fmt.Fprintf(out, "generating ED-25519 keypair with %d matching prefix ...", difficulty)
	sk, pk := generateEclipseKeyPairParallel("QmdmQXB2mzChmMeKY47C43LxUdg1NDJ5MWcKMKxDu7RgQm")
	fmt.Fprintf(out, "done\n")

	// currently storing key unencrypted. in the future we need to encrypt it.
	// TODO(security)
	skbytes, err := sk.Bytes()
	if err != nil {
		return ident, err
	}
	ident.PrivKey = base64.StdEncoding.EncodeToString(skbytes)

	id, err := peer.IDFromPublicKey(pk)
	if err != nil {
		return ident, err
	}
	ident.PeerID = id.Pretty()
	fmt.Fprintf(out, "peer identity: %s\n", ident.PeerID)
	return ident, nil
}

func generateEclipseKeyPairParallel(destPrettyID string) (ci.PrivKey, ci.PubKey) {
	numWorkers := runtime.NumCPU()
	runtime.GOMAXPROCS(numWorkers)
	keyChan := make(chan eclipseKey)

	for i := 0; i < numWorkers; i++ {
		go func() {
			err := generateEclipseKeyPair(destPrettyID, keyChan)
			if err != nil {
				log.Fatal(err)
			}
			close(keyChan)
		}()
	}

	keyPair := <-keyChan

	return keyPair.priv, keyPair.pub
}

func generateEclipseKeyPair(destPrettyID string, keyChan chan eclipseKey) error {
	for {
		privateKey, publicKey, err := ci.GenerateEd25519Key(rand.Reader)
		//privateKey, publicKey, err := ci.GenerateRSAKeyPair(2048, rand.Reader)
		if err != nil {
			return err
		}

		genID, err := peer.IDFromPublicKey(publicKey)
		if err != nil {
			return err
		}

		genPretty := genID.Pretty()

		matchPrefix := matchingPrefix(genPretty, destPrettyID)

		if matchPrefix < difficulty {
			continue
		}

		keyChan <- eclipseKey{
			priv: privateKey,
			pub:  publicKey,
		}
		break
	}
	return nil
}

func byteArrayToInt(byteSlice []byte, bytes int) int {
	sum := 0
	for i := 0; i < bytes; i++ {
		sum = sum + power(2, ((bytes-i-1)*8))*int(byteSlice[i])
	}

	return sum
}

func power(a, n int) int {
	var i, result int
	result = 1
	for i = 0; i < n; i++ {
		result *= a
	}
	return result
}

func matchingPrefix(a, b string) int {
	id1, err := peer.IDB58Decode(a)
	if err != nil {
		fmt.Println("converting ID 1 failed: ", err)
	}

	id2, err := peer.IDB58Decode(b)
	if err != nil {
		fmt.Println("converting ID 2 failed: ", err)
	}

	xor := u.XOR(kb.ConvertPeerID(id1), kb.ConvertPeerID(id2))

	xorInt := byteArrayToInt(xor, 4)

	leadingZeros := bits.LeadingZeros32(uint32(xorInt))
	return leadingZeros
}
