package main

import (
	"context"
	"crypto/ecdsa"
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
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	flags "github.com/jessevdk/go-flags"
)

var opts struct {
	RPCURLs           []string `env:"RPC_URL" long:"rpc-url" required:"true" description:"Ethereum clients urls"`
	ContractAddresses []string `env:"CONTRACT_ADDRESS" long:"contract-address" description:"ERC20 contracts addresses"`
	PrivateKeys       []string `env:"PRIVATE_KEY" long:"private-key" required:"true" description:"Base64URL encoded private keys"`
	SwipeAddress      string   `env:"SWIPE_ADDRESS" long:"swipe-address" description:"Swipe address"`
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
		return "Ether", "ETH", 18
	}

	_decimals, err := erc20.Decimals(&bind.CallOpts{})
	check(err)

	name, err = erc20.Name(&bind.CallOpts{})
	if err != nil {
		name = "Non ERC20 strict"
	}

	symbol, err = erc20.Symbol(&bind.CallOpts{})
	if err != nil {
		symbol = "ERC20"
	}

	decimals = uint(_decimals)
	return
}

func SwipeToERC20(ctx context.Context, c *ethclient.Client, erc20Addr common.Address, fromKey *ecdsa.PrivateKey, to common.Address, value, networkId *big.Int) common.Hash {
	erc20, err := NewERC20Transactor(erc20Addr, c)
	check(err)
	signedTx, err := erc20.Transfer(bind.NewKeyedTransactor(fromKey), to, value)
	check(err)
	from := crypto.PubkeyToAddress(fromKey.PublicKey)
	log.Printf("Swipping ERC20 from %s to %s amount: %s [%s]", from.String(), to.String(), value, signedTx.Hash().String())
	return signedTx.Hash()
}

func SwipeTo(ctx context.Context, c *ethclient.Client, fromKey *ecdsa.PrivateKey, to common.Address, value, networkId *big.Int) {
	from := crypto.PubkeyToAddress(fromKey.PublicKey)
	nonce, err := c.NonceAt(ctx, from, nil)
	check(err)
	gasLimit := big.NewInt(21000)
	gasPrice, err := c.SuggestGasPrice(ctx)
	check(err)
	if gasPrice.Cmp(&big.Int{}) == 0 {
		gasPrice = new(big.Int).Mul(big.NewInt(110000), big.NewInt(10000))
	}
	var data []byte
	newValue := new(big.Int).Sub(value, new(big.Int).Mul(gasPrice, gasLimit))
	tx := types.NewTransaction(nonce, to, newValue, gasLimit.Uint64(), gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(networkId), fromKey)
	check(err)
	err = c.SendTransaction(ctx, signedTx)
	check(err)
	log.Printf("Swipping amount: %s (%s fee) [%s]", newValue, gasPrice, signedTx.Hash().String())
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

	var privateKeys []*ecdsa.PrivateKey
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
		privateKeys = append(privateKeys, key)
	}

	var swipeTo common.Address
	if opts.SwipeAddress != "" {
		swipeTo = common.HexToAddress(opts.SwipeAddress)
		log.Printf("Swipping all account to %s\n", swipeTo.String())
	}
	for _, rpcUrl := range opts.RPCURLs {
		c, err := ethclient.Dial(rpcUrl)
		if err != nil {
			log.Println(err)
			continue
		}
		networkId, err := c.NetworkID(ctx)
		check(err)

		log.Printf("Connected to %v [network id: %s]", rpcUrl, networkId)

		for _, key := range privateKeys {
			from := crypto.PubkeyToAddress(key.PublicKey)
			for _, contractAddr := range contractAddresses {
				erc20, err := NewERC20Caller(contractAddr, c)
				if err != nil {
					log.Println(err)
					continue
				}
				bal, err := erc20.BalanceOf(&bind.CallOpts{}, from)
				if err != nil {
					continue
				}
				if bal.Cmp(&big.Int{}) == 0 {
					continue
				}
				name, unit, dec := getERC20Info(c, erc20)
				fmt.Printf("%v [%v]: \n", name, contractAddr.String())
				printAccount(from, unit, dec, bal)
				// Do not swipe tokens…
				//if swipeTo != *new(common.Address) {
				//	SwipeToERC20(ctx, c, contractAddr, key, swipeTo, bal, networkId)
				//}
			}
			bal, err := c.BalanceAt(ctx, from, nil)
			check(err)
			_, unit, dec := getERC20Info(c, nil)
			if bal.Cmp(&big.Int{}) != 0 {
				printAccount(from, unit, dec, bal)
				if swipeTo != *new(common.Address) {
					SwipeTo(ctx, c, key, swipeTo, bal, networkId)
				}
			}
		}
	}
}
