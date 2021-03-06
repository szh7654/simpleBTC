package BLC

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"github.com/boltdb/bolt"
	"log"
	"math/big"
	"os"
	"strconv"
)

type Blockchain struct {
	Tip []byte   // BlockHash of top Block
	DB  *bolt.DB // A pointer to the database
}

func CreateBlockchainWithGenesisBlock(address string, nodeID string) {
	DBName := fmt.Sprintf(DBName, nodeID)
	if DBExists(DBName) {
		fmt.Println("Genesis block already exist!")
		os.Exit(1)
	}

	fmt.Println("Creating genesis block....")

	db, err := bolt.Open(DBName, 0600, nil)
	if err != nil {
		log.Panic(err)
	}

	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(BlockBucketName))
		if err != nil {
			log.Panic(err)
		}
		if b != nil {
			// Create the genesis block with a coinbase transaction
			txCoinbase := NewCoinbaseTransacion(address)
			genesisBlock := CreateGenesisBlock([]*Transaction{txCoinbase})

			err := b.Put(genesisBlock.BlockHash, gobEncode(genesisBlock))
			if err != nil {
				log.Panic(err)
			}
			// Update Tip of blockchain
			err = b.Put([]byte("l"), genesisBlock.BlockHash)
			if err != nil {
				log.Panic(err)
			}
		}
		return nil
	})

	if err != nil {
		log.Panic(err)
	}
}

// Convert command variables to Transaction Objects
func (blockchain *Blockchain) hanldeTransations(from []string, to []string, amount []string, nodeId string) []*Transaction {
	var txs []*Transaction
	utxoSet := &UTXOSet{blockchain}

	for i := 0; i < len(from); i++ {
		amountInt, _ := strconv.Atoi(amount[i])
		tx := NewSimpleTransation(from[i], to[i], int64(amountInt), utxoSet, txs, nodeId)
		txs = append(txs, tx)
	}
	return txs
}

// Package transactions and mine a new Block
func (blockchain *Blockchain) MineNewBlock(originalTxs []*Transaction) *Block {
	// Reward of mining a block
	coinBaseTransaction := NewRewardTransacion()
	txs := []*Transaction{coinBaseTransaction}
	txs = append(txs, originalTxs...)
	// Verify transactions
	for _, tx := range txs {
		if !tx.IsCoinBaseTransaction() {
			if blockchain.VerifityTransaction(tx, txs) == false {
				log.Panic("Verify transaction failed...")
			}
		}
	}

	DBName := fmt.Sprintf(DBName, os.Getenv("NODE_ID"))
	db, err := bolt.Open(DBName, 0600, nil)
	if err != nil {
		log.Panic(err)
	}
	defer db.Close()
	// Get the latest block
	var block Block
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BlockBucketName))
		if b != nil {
			hash := b.Get([]byte("l"))
			blockBytes := b.Get(hash)
			gobDecode(blockBytes, &block)
		}
		return nil
	})
	if err != nil {
		log.Panic(err)
	}

	// Mine a new block
	newBlock := NewBlock(txs, block.Height+1, block.BlockHash)

	return newBlock
}

// Save a block to the database
func (blockchain *Blockchain) SaveNewBlockToBlockchain(newBlock *Block) {
	DBName := fmt.Sprintf(DBName, os.Getenv("NODE_ID"))
	db, err := bolt.Open(DBName, 0600, nil)
	if err != nil {
		log.Panic(err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BlockBucketName))
		if b != nil {
			b.Put(newBlock.BlockHash, gobEncode(newBlock))
			b.Put([]byte("l"), newBlock.BlockHash)
			blockchain.Tip = newBlock.BlockHash
		}
		return nil
	})

	if err != nil {
		log.Panic(err)
	}
}

// Get Unspent transaction outputs(UTXOs)
func (blc *Blockchain) getUTXOsByAddress(address string, txs []*Transaction) []*UTXO {
	var utxos []*UTXO
	spentTxOutputMap := make(map[string][]int)
	// calculate UTXOs by querying txs
	for i := len(txs) - 1; i >= 0; i-- {
		utxos = caculate(txs[i], address, spentTxOutputMap, utxos)
	}

	// calculate UTXOs by querying Blocks
	it := blc.Iterator()
	for {
		block := it.Next()
		for i := len(block.Transactions) - 1; i >= 0; i-- {
			utxos = caculate(block.Transactions[i], address, spentTxOutputMap, utxos)
		}
		hashInt := new(big.Int)
		hashInt.SetBytes(block.PrevBlockHash)
		// If current block is genesis block, exit loop
		if big.NewInt(0).Cmp(hashInt) == 0 {
			break
		}
	}
	return utxos
}

// calculate utxos 
func caculate(tx *Transaction, address string, spentOutputMap map[string][]int, utxos []*UTXO) []*UTXO {
	// collect all inputs into spentOutputMap
	if !tx.IsCoinBaseTransaction() {
		for _, input := range tx.Inputs {
			full_payload := Base58Decode([]byte(address))
			pubKeyHash := full_payload[1 : len(full_payload)-addressCheckSumLen]
			if input.UnlockWithAddress(pubKeyHash) {
				transactionHash := hex.EncodeToString(input.TransactionHash)
				spentOutputMap[transactionHash] = append(spentOutputMap[transactionHash], input.IndexOfOutputs)
			}
		}
	}

	// Tranverse all outputs, unSpentUTXOs = all outputs - spent outputs
outputsLoop:
	for index, output := range tx.Outputs {
		if output.UnlockWithAddress(address) {
			if len(spentOutputMap) != 0 {
				var isSpent bool
				for transactionHash, indexArray := range spentOutputMap { //143d,[]int{1}
					//?????? ????????????????????????????????????
					for _, i := range indexArray {
						if i == index && hex.EncodeToString(tx.TransactionHash) == transactionHash {
							isSpent = true //???????????????output???????????????
							continue outputsLoop
						}
					}
				}

				if !isSpent {
					utxo := &UTXO{tx.TransactionHash, index, output}
					utxos = append(utxos, utxo)
				}

			} else {
				utxo := &UTXO{tx.TransactionHash, index, output}
				utxos = append(utxos, utxo)
			}
		}
	}
	return utxos
}

// Find UTXOs which can be regarded as inputs in this transaction
func (bc *Blockchain) FindSpendableUTXOs(from string, amount int64, txs []*Transaction) (int64, map[string][]int) {
	var total int64
	spendableMap := make(map[string][]int)
	utxos := bc.getUTXOsByAddress(from, txs)

	for _, utxo := range utxos {
		total += utxo.Output.Value
		transactionHash := hex.EncodeToString(utxo.TransactionHash)
		spendableMap[transactionHash] = append(spendableMap[transactionHash], utxo.Index)
		if total >= amount {
			break
		}
	}

	if total < amount {
		fmt.Printf("%s????????????????????????????????????", from)
		os.Exit(1)
	}

	return total, spendableMap
}

func (blc *Blockchain) Printchain() {
	blockIterator := blc.Iterator()
	for {
		block := blockIterator.Next()
		fmt.Println(block)
		var hashInt big.Int
		hashInt.SetBytes(block.PrevBlockHash)
		if big.NewInt(0).Cmp(&hashInt) == 0 {
			break
		}
	}
}

func (blockchain *Blockchain) Iterator() *BlockchainIterator {
	return &BlockchainIterator{blockchain.Tip, blockchain.DB}
}

func DBExists(DBName string) bool {
	if _, err := os.Stat(DBName); os.IsNotExist(err) {
		return false
	}
	return true
}

func BlockchainObject(nodeID string) *Blockchain {
	DBName := fmt.Sprintf(DBName, nodeID)
	if DBExists(DBName) {
		db, err := bolt.Open(DBName, 0600, nil)
		if err != nil {
			log.Panic(err)
		}
		defer db.Close()
		var blockchain *Blockchain
		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(BlockBucketName))
			if b != nil {
				hash := b.Get([]byte("l"))
				blockchain = &Blockchain{hash, db}
			}
			return nil
		})
		if err != nil {
			log.Panic(err)
		}
		return blockchain
	} else {
		fmt.Println("?????????????????????????????????BlockChain???????????????")
		return nil
	}
}

func (bc *Blockchain) SignTransaction(tx *Transaction, privateKey ecdsa.PrivateKey, txs []*Transaction) {
	if tx.IsCoinBaseTransaction() {
		return
	}
	prevTransactionMap := make(map[string]*Transaction)
	for _, input := range tx.Inputs {
		transactionHash := hex.EncodeToString(input.TransactionHash)
		prevTransactionMap[transactionHash] = bc.FindTransactionByTransactionHash(input.TransactionHash, txs)
	}
	tx.Sign(privateKey, prevTransactionMap)
}

func (bc *Blockchain) FindTransactionByTransactionHash(transactionHash []byte, txs []*Transaction) *Transaction {
	for _, tx := range txs {
		if bytes.Compare(tx.TransactionHash, transactionHash) == 0 {
			return tx
		}
	}
	iterator := bc.Iterator()
	for {
		block := iterator.Next()
		for _, tx := range block.Transactions {
			if bytes.Compare(tx.TransactionHash, transactionHash) == 0 {
				return tx
			}
		}
		bigInt := new(big.Int)
		bigInt.SetBytes(block.PrevBlockHash)
		if big.NewInt(0).Cmp(bigInt) == 0 {
			break
		}
	}
	return &Transaction{}
}

/*
	???????????????????????????
*/
func (bc *Blockchain) VerifityTransaction(tx *Transaction, txs []*Transaction) bool {
	//?????????????????????????????????+?????? (tx?????????+???????????????)
	//2.?????????tx??????Input??????????????????transaction??????????????????output
	prevTxs := make(map[string]*Transaction)
	for _, input := range tx.Inputs {
		transactionHash := hex.EncodeToString(input.TransactionHash)
		prevTxs[transactionHash] = bc.FindTransactionByTransactionHash(input.TransactionHash, txs)
	}

	if len(prevTxs) == 0 {
		fmt.Println("?????????????????????")
	} else {
		//fmt.Println("preTxs___________________________________")
		//fmt.Println(prevTxs)
	}

	//??????
	return tx.VerifyTransaction(prevTxs)
	//return true
}

func (bc *Blockchain) GetAllUTXOs() map[string]*UTXOArray {
	iterator := bc.Iterator()
	utxoMap := make(map[string]*UTXOArray)
	//????????????input map
	inputMap := make(map[string][]*Input)

	for {
		block := iterator.Next()
		for i := len(block.Transactions) - 1; i >= 0; i-- {
			// collect inputs
			tx := block.Transactions[i]                               
			transactionHash := hex.EncodeToString(tx.TransactionHash)
			utxoArray := &UTXOArray{[]*UTXO{}}
			if !tx.IsCoinBaseTransaction() {
				for _, input := range tx.Inputs {
					transactionHash := hex.EncodeToString(input.TransactionHash)
					inputMap[transactionHash] = append(inputMap[transactionHash], input)
				}
			}

			//??????inputMap,??????outputs ?????? UTXO
			outputLoop:
			for index, output := range tx.Outputs {

				if len(inputMap) > 0 {
					//isSpent := false
					inputs := inputMap[transactionHash] //??????inputs ??????, ??????????????????????????????output?????????????????????
					for _, input := range inputs {
						//??????input????????????????????????output
						if index == input.IndexOfOutputs && input.UnlockWithAddress(output.PubKeyHash) {
							//??????output????????????
							//isSpent = true
							continue outputLoop
						}
					}

					//if isSpent == false {
					//outputs ??????utxoMap
					utxo := &UTXO{tx.TransactionHash, index, output}
					utxoArray.UTXOs = append(utxoArray.UTXOs, utxo)
					//}
				} else {
					//outputs ??????utxoMap
					utxo := &UTXO{tx.TransactionHash, index, output}
					utxoArray.UTXOs = append(utxoArray.UTXOs, utxo)
				}
			}

			if len(utxoArray.UTXOs) > 0 {
				utxoMap[transactionHash] = utxoArray
			}

		}

		//????????????
		hashBigInt := new(big.Int)
		hashBigInt.SetBytes(block.PrevBlockHash)
		if big.NewInt(0).Cmp(hashBigInt) == 0 {
			break
		}
	}

	return utxoMap
}

func (bc *Blockchain) GetHeight() int64 {
	return bc.Iterator().Next().Height
}

func (bc *Blockchain) getAllBlocksHash() [][]byte {
	iterator := bc.Iterator()
	var blocksHashes [][]byte
	for {
		block := iterator.Next()
		blocksHashes = append(blocksHashes, block.BlockHash)
		bigInt := new(big.Int)
		bigInt.SetBytes(block.PrevBlockHash)
		if big.NewInt(0).Cmp(bigInt) == 0 {
			break
		}
	}
	return blocksHashes
}

func (bc *Blockchain) GetBlockByHash(hash []byte) *Block {
	var block Block

	DBName := fmt.Sprintf(DBName, os.Getenv("NODE_ID"))
	db, err := bolt.Open(DBName, 0600, nil)
	if err != nil {
		log.Panic(err)
	}
	defer db.Close()
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BlockBucketName))
		if b != nil {
			blockBytes := b.Get(hash)
			gobDecode(blockBytes, &block)
		}
		return nil
	})

	if err != nil {
		log.Panic(err)
	}
	return &block
}

func (bc *Blockchain) AddBlockToChain(block *Block) {
	DBName := fmt.Sprintf(DBName, os.Getenv("NODE_ID"))
	db, err := bolt.Open(DBName, 0600, nil)
	if err != nil {
		log.Panic(err)
	}
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BlockBucketName))
		if b != nil {
			blockBytes := b.Get(block.BlockHash)
			if blockBytes != nil {
				return nil
			}
			err := b.Put(block.BlockHash, gobEncode(block))
			if err != nil {
				log.Panic(err)
			}
			
			lastBlockHash := b.Get([]byte("l"))
			lastBlockBytes := b.Get(lastBlockHash)
			var lastBlock Block
			gobDecode(lastBlockBytes, &lastBlock)
			if lastBlock.Height < block.Height {
				b.Put([]byte("l"), block.BlockHash)
				bc.Tip = block.BlockHash
			}
		}
		return nil
	})
	if err != nil {
		log.Panic(err)
	}
}
