package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	flags "github.com/jessevdk/go-flags"
)

var opts struct {
	RPCURLs           []string `env:"RPC_URL" long:"rpc-url" required:"true" description:"Ethereum clients urls"`
	ContractAddresses []string `env:"CONTRACT_ADDRESS" long:"contract-address" required:"true" description:"ERC20 contracts addresses"`
	PrivateKeys       []string `env:"PRIVATE_KEY" long:"private-key" required:"true" description:"Base64URL encoded private keys"`
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func printAccount(from common.Address, unit string, dec uint, balance *big.Int) {
	fbal := &big.Float{}
	fbal, ok := fbal.SetString(balance.String())
	if !ok {
		panic("Invalid balance value, got " + balance.String())
	}
	value := new(big.Float).Quo(fbal, big.NewFloat(math.Pow10(int(dec))))

	fmt.Printf("%s, balance: %v %s\n", from.Hex(), value.String(), unit)
}

func getERC20Info(c *ethclient.Client, erc20 *ERC20Caller) (name string, symbol string, decimals uint) {
	var err error
	if erc20 == nil {
		return "Ether", "eth", 18
	}
	name, err = erc20.Name(&bind.CallOpts{})
	if err != nil {
		name = "Unknown"
	}

	symbol, err = erc20.Symbol(&bind.CallOpts{})
	if err != nil {
		symbol = "Unk"
	}

	_decimals, err := erc20.Decimals(&bind.CallOpts{})
	check(err)
	decimals = uint(_decimals)
	return
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancelFunc := context.WithCancel(context.Background())
	go func() {
		<-sigs
		cancelFunc()
		log.Fatal("Exit")
	}()
	_, err := flags.Parse(&opts)
	check(err)
	var contractAddresses []common.Address
	for _, contractAddr := range opts.ContractAddresses {
		contractAddresses = append(contractAddresses, common.HexToAddress(contractAddr))
	}
	var addresses []common.Address
	for _, privateKey := range opts.PrivateKeys {
		str := privateKey
		if strings.ContainsAny(str, "+/") {
			log.Println("Bad private key, got:", privateKey)
			panic("invalid base64url encoding")
		}
		str = strings.Replace(str, "-", "+", -1)
		str = strings.Replace(str, "_", "/", -1)
		str = strings.TrimSpace(str)
		for len(str)%4 != 0 {
			str += "="
		}
		pkey, err := base64.StdEncoding.DecodeString(str)
		if err != nil {
			log.Println("Bad private key, got:", privateKey, "->")
			check(err)
		}
		key, err := crypto.ToECDSA(pkey)
		check(err)
		addresses = append(addresses, crypto.PubkeyToAddress(key.PublicKey))
	}
	for _, rpcUrl := range opts.RPCURLs {
		c, err := ethclient.Dial(rpcUrl)
		if err != nil {
			log.Println(err)
			continue
		}
		log.Printf("Connected to %v", rpcUrl)
		for _, from := range addresses {
			bal, err := c.BalanceAt(ctx, from, nil)
			check(err)
			_, unit, dec := getERC20Info(c, nil)
			printAccount(from, unit, dec, bal)
			for _, contractAddr := range contractAddresses {
				erc20, err := NewERC20Caller(contractAddr, c)
				if err != nil {
					log.Println(err)
					continue
				}
				bal, err = erc20.BalanceOf(&bind.CallOpts{}, from)
				if err != nil {
					continue
				}
				if bal.Cmp(&big.Int{}) == 0 {
					continue
				}
				name, unit, dec := getERC20Info(c, erc20)
				fmt.Printf("%v [%v]: \n", name, contractAddr.String())
				printAccount(from, unit, dec, bal)
			}
		}
	}
}
