package main

import (
	"context"
	"flag"
	"github.com/coreos/etcd/client"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	docker "github.com/docker/docker/client"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/backend/etcd"
	calico "github.com/projectcalico/libcalico-go/lib/client"
	"log"
	"strings"
	"time"
	"net"
)

var (
	etcdEndpoint, dockerHost string
	mask int
)

func init() {
	flag.StringVar(&etcdEndpoint, "etcd", "", "etcd endpoint")
	flag.StringVar(&dockerHost, "docker", "", "docker host")
	flag.IntVar(&mask,"mask",24,"mask")
	flag.Parse()
}

func main() {
	networkMap := make(map[string]string)
	dockerClient, err := docker.NewClient("tcp://"+dockerHost, "1.24", nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	cfg := client.Config{
		Endpoints:               []string{"http://" + etcdEndpoint},
		Transport:               client.DefaultTransport,
		HeaderTimeoutPerRequest: time.Second,
	}
	etcdClient, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	kapi := client.NewKeysAPI(etcdClient)

	opt:= &client.SetOptions{
		Dir: true,
	}

	kapi.Set(context.Background(),"/calico/bgp/v1/global/custom_filters/v4","",opt)
	kapi.Set(context.Background(),"/calico/bgp/v1/global/custom_filters/v6","",opt)

	networkFilters := filters.NewArgs()
	networkFilters.Add("driver", "calico")
	networks, err := dockerClient.NetworkList(context.Background(), types.NetworkListOptions{
		Filters: networkFilters,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, network := range networks {

		networkMap[network.ID] = network.Name
		log.Println("network id [" + network.ID + "] name [" + network.Name + "]")

		resp, err := kapi.Get(context.Background(), "/docker/network/v1.0/network/"+network.ID, nil)
		if err != nil {
			log.Fatal(err)
		}
		v := strings.Replace(resp.Node.Value, "\"ipamType\":\"calico\"", "\"ipamType\":\"calico-ipam\"", -1)
		_, err = kapi.Set(context.Background(), "/docker/network/v1.0/network/"+network.ID, v, nil)
		if err != nil {
			log.Fatal(err)
		}
	}

	config := api.CalicoAPIConfig{
		Spec: api.CalicoAPIConfigSpec{
			DatastoreType: api.EtcdV2,
			EtcdConfig: etcd.EtcdConfig{
				EtcdEndpoints: "http://" + etcdEndpoint,
			},
		},
	}

	c, _ := calico.New(config)

	profileList, _ := c.Profiles().List(api.ProfileMetadata{})

	for _, profile := range profileList.Items {
		if v, ok := networkMap[profile.Metadata.Name]; ok {
			for i := 0; i < len(profile.Spec.IngressRules); i++ {
				if profile.Spec.IngressRules[i].Source.Tag != "" {
					if v, ok := networkMap[profile.Spec.IngressRules[i].Source.Tag]; ok {
						profile.Spec.IngressRules[i].Source.Tag = v
					} else {
						log.Println("profile " + profile.Metadata.Name + " ingress rule source tag [" + networkMap[profile.Spec.IngressRules[i].Source.Tag] + "] not found in network map")
					}
				}
				if profile.Spec.IngressRules[i].Destination.Tag != "" {
					if v, ok := networkMap[profile.Spec.IngressRules[i].Destination.Tag]; ok {
						profile.Spec.IngressRules[i].Destination.Tag = v
					} else {
						log.Println("profile " + profile.Metadata.Name + " ingress rule destination tag [" + networkMap[profile.Spec.IngressRules[i].Destination.Tag] + "] not found in network map")
					}
				}
			}
			for i := 0; i < len(profile.Spec.EgressRules); i++ {
				if profile.Spec.EgressRules[i].Source.Tag != "" {
					if v, ok := networkMap[profile.Spec.EgressRules[i].Source.Tag]; ok {
						profile.Spec.EgressRules[i].Source.Tag = v
					} else {
						log.Println("profile " + profile.Metadata.Name + " egress rule source tag [" + networkMap[profile.Spec.EgressRules[i].Source.Tag] + "] not found in network map")
					}
				}
				if profile.Spec.EgressRules[i].Destination.Tag != "" {
					if v, ok := networkMap[profile.Spec.EgressRules[i].Destination.Tag]; ok {
						profile.Spec.EgressRules[i].Destination.Tag = v
					} else {
						log.Println("profile " + profile.Metadata.Name + " egress rule destination tag [" + networkMap[profile.Spec.EgressRules[i].Destination.Tag] + "] not found in network map")
					}
				}
			}
			c.Profiles().Delete(profile.Metadata)
			profile.Metadata.Name = v
			for i,tag := range profile.Metadata.Tags {
				if v, ok := networkMap[tag]; ok {
					profile.Metadata.Tags[i] = v
				}
			}
			c.Profiles().Apply(&profile)
		} else {
			log.Println("profile name [" + networkMap[profile.Metadata.Name] + "] not found in network map")
		}
	}

	workloadEndpointList, err := c.WorkloadEndpoints().List(api.WorkloadEndpointMetadata{})
	if err != nil {
		log.Fatal(err)
	}
	for _, workloadEndPoint := range workloadEndpointList.Items {
		if len(workloadEndPoint.Spec.Profiles) == 1 {
			if v, ok := networkMap[workloadEndPoint.Spec.Profiles[0]]; ok {
				workloadEndPoint.Spec.Profiles[0] = v
				c.WorkloadEndpoints().Update(&workloadEndPoint)
			} else {
				log.Println("workload endpoint profile id [" + workloadEndPoint.Spec.Profiles[0] + "] not found in network map")
			}
		} else {
			log.Printf("len(workloadEndPoint.Spec.Profiles) != 1 %v", workloadEndPoint.Spec.Profiles)
		}
	}

	nodeList, _ := c.Nodes().List(api.NodeMetadata{})
	for _,node := range nodeList.Items {
		node.Spec.BGP.IPv4Address.Mask = net.CIDRMask(mask, 32)
		c.Nodes().Apply(&node)
	}
}
