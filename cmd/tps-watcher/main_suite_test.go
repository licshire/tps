package main_test

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudfoundry-incubator/consuladapter"
	receptorrunner "github.com/cloudfoundry-incubator/receptor/cmd/receptor/testrunner"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/tps/cmd/tpsrunner"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"testing"
)

var (
	receptorPath string
	receptorPort int

	etcdPort int

	consulRunner *consuladapter.ClusterRunner

	watcher ifrit.Process
	runner  *ginkgomon.Runner

	watcherPath string

	fakeCC         *ghttp.Server
	etcdRunner     *etcdstorerunner.ETCDClusterRunner
	receptorRunner ifrit.Process
	store          storeadapter.StoreAdapter
	bbs            *Bbs.BBS
	logger         *lagertest.TestLogger
)

const assetsPath = "../../../../cloudfoundry/storeadapter/assets/"

func TestTPS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "TPS-Watcher Suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	tps, err := gexec.Build("github.com/cloudfoundry-incubator/tps/cmd/tps-watcher", "-race")
	Expect(err).NotTo(HaveOccurred())

	receptor, err := gexec.Build("github.com/cloudfoundry-incubator/receptor/cmd/receptor", "-race")
	Expect(err).NotTo(HaveOccurred())

	payload, err := json.Marshal(map[string]string{
		"watcher":  tps,
		"receptor": receptor,
	})
	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(payload []byte) {
	binaries := map[string]string{}

	err := json.Unmarshal(payload, &binaries)
	Expect(err).NotTo(HaveOccurred())

	etcdPort = 5001 + GinkgoParallelNode()
	receptorPort = 6001 + GinkgoParallelNode()*2

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(etcdPort, 1,
		&etcdstorerunner.SSLConfig{
			CertFile: assetsPath + "server.crt",
			KeyFile:  assetsPath + "server.key",
			CAFile:   assetsPath + "ca.crt",
		})

	watcherPath = string(binaries["watcher"])
	receptorPath = string(binaries["receptor"])
	store = etcdRunner.Adapter(
		&etcdstorerunner.SSLConfig{
			CertFile: assetsPath + "client.crt",
			KeyFile:  assetsPath + "client.key",
			CAFile:   assetsPath + "ca.crt",
		})

	consulRunner = consuladapter.NewClusterRunner(
		9001+config.GinkgoConfig.ParallelNode*consuladapter.PortOffsetLength,
		1,
		"http",
	)

	logger = lagertest.NewTestLogger("test")
})

var _ = BeforeEach(func() {
	etcdRunner.Start()

	consulRunner.Start()
	consulRunner.WaitUntilReady()

	taskHandlerAddress := fmt.Sprintf("127.0.0.1:%d", receptorPort+1)
	bbs = Bbs.NewBBS(store, consulRunner.NewSession("a-session"), "http://"+taskHandlerAddress, clock.NewClock(), logger)

	receptor := receptorrunner.New(receptorPath, receptorrunner.Args{
		Address:            fmt.Sprintf("127.0.0.1:%d", receptorPort),
		TaskHandlerAddress: taskHandlerAddress,
		EtcdCluster:        strings.Join(etcdRunner.NodeURLS(), ","),
		ConsulCluster:      consulRunner.ConsulCluster(),
		ClientCert:         assetsPath + "client.crt",
		ClientKey:          assetsPath + "client.key",
		CACert:             assetsPath + "ca.crt",
	})
	receptorRunner = ginkgomon.Invoke(receptor)

	fakeCC = ghttp.NewServer()

	runner = tpsrunner.NewWatcher(
		string(watcherPath),
		fmt.Sprintf("http://127.0.0.1:%d", receptorPort),
		fmt.Sprintf(fakeCC.URL()),
		consulRunner.ConsulCluster(),
	)
})

var _ = AfterEach(func() {
	fakeCC.Close()
	ginkgomon.Kill(receptorRunner, 5)
	etcdRunner.Stop()
	consulRunner.Stop()
})

var _ = SynchronizedAfterSuite(func() {
}, func() {
	gexec.CleanupBuildArtifacts()
})
