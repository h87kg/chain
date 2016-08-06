package account_test

import (
	"reflect"
	"testing"
	"time"

	"golang.org/x/net/context"

	. "chain/core/account"
	"chain/core/asset/assettest"
	"chain/core/txbuilder"
	"chain/cos/bc"
	"chain/database/pg"
	"chain/database/pg/pgtest"
	"chain/errors"
)

func TestAccountSourceReserve(t *testing.T) {
	ctx := pg.NewContext(context.Background(), pgtest.NewTx(t))
	_, _, err := assettest.InitializeSigningGenerator(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	accID := assettest.CreateAccountFixture(ctx, t, nil, 0, nil)
	asset := assettest.CreateAssetFixture(ctx, t, nil, 0, nil)
	out := assettest.IssueAssetsFixture(ctx, t, asset, 2, accID)

	assetAmount1 := &bc.AssetAmount{
		AssetID: asset,
		Amount:  1,
	}
	source := NewSource(ctx, assetAmount1, accID, nil, nil, nil)

	got, err := source.Reserver.Reserve(ctx, assetAmount1, time.Minute)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}

	want := &txbuilder.ReserveResult{
		Items: []*txbuilder.ReserveResultItem{{
			TxInput:       bc.NewSpendInput(out.Hash, out.Index, nil, out.AssetID, out.Amount, out.ControlProgram, nil),
			TemplateInput: nil,
		}},
		Change: []*txbuilder.Destination{{
			AssetAmount: bc.AssetAmount{AssetID: asset, Amount: 1},
		}},
	}

	if len(got.Items) != 1 {
		t.Fatalf("expected 1 result utxo")
	}

	// generated address can change based on test ordering, so ignore in comparison
	got.Items[0].TemplateInput = nil

	ar, ok := got.Change[0].Receiver.(*Receiver)
	if !ok {
		t.Fatalf("expected change destination to have Receiver")
	}

	if ar.AccountID() != accID {
		t.Errorf("got receiver addr account = %v want %v", ar.AccountID(), accID)
	}

	// clear out to not compare generated addresses
	got.Change[0].Receiver = nil

	if !reflect.DeepEqual(got, want) {
		t.Errorf("reserve result\ngot:\n\t%+v\nwant:\n\t%+v", got, want)
		t.Errorf("reserve item\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0], want.Items[0])
		t.Errorf("reserve txin\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0].TxInput, want.Items[0].TxInput)
		t.Errorf("reserve change\ngot:\n\t%+v\nwant:\n\t%+v", got.Change, want.Change)
	}
}

func TestAccountSourceUTXOReserve(t *testing.T) {
	ctx := pg.NewContext(context.Background(), pgtest.NewTx(t))
	_, _, err := assettest.InitializeSigningGenerator(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	accID := assettest.CreateAccountFixture(ctx, t, nil, 0, nil)
	asset := assettest.CreateAssetFixture(ctx, t, nil, 0, nil)
	out := assettest.IssueAssetsFixture(ctx, t, asset, 2, accID)

	assetAmount1 := &bc.AssetAmount{
		AssetID: asset,
		Amount:  1,
	}
	source := NewSource(ctx, assetAmount1, accID, &out.Outpoint.Hash, &out.Outpoint.Index, nil)

	got, err := source.Reserver.Reserve(ctx, assetAmount1, time.Minute)
	if err != nil {
		t.Log(errors.Stack(err))
		t.Fatal(err)
	}

	want := &txbuilder.ReserveResult{
		Items: []*txbuilder.ReserveResultItem{{
			TxInput:       bc.NewSpendInput(out.Hash, out.Index, nil, out.AssetID, out.Amount, out.ControlProgram, nil),
			TemplateInput: nil,
		}},
		Change: []*txbuilder.Destination{{
			AssetAmount: bc.AssetAmount{AssetID: asset, Amount: 1},
		}},
	}

	if len(got.Items) != 1 {
		t.Fatalf("expected 1 result utxo")
	}

	// generated address can change based on test ordering, so ignore in comparison
	got.Items[0].TemplateInput = nil

	ar, ok := got.Change[0].Receiver.(*Receiver)
	if !ok {
		t.Fatalf("expected change destination to have Receiver")
	}

	if ar.AccountID() != accID {
		t.Errorf("got receiver account id = %v want %v", ar.AccountID(), accID)
	}

	// clear out to not compare generated addresses
	got.Change[0].Receiver = nil

	if !reflect.DeepEqual(got, want) {
		t.Errorf("reserve result\ngot:\n\t%+v\nwant:\n\t%+v", got, want)
		t.Errorf("reserve item\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0], want.Items[0])
		t.Errorf("reserve txin\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0].TxInput, want.Items[0].TxInput)
		t.Errorf("reserve change\ngot:\n\t%+v\nwant:\n\t%+v", got.Change, want.Change)
	}
}

func TestAccountSourceReserveIdempotency(t *testing.T) {
	ctx := pg.NewContext(context.Background(), pgtest.NewTx(t))
	_, _, err := assettest.InitializeSigningGenerator(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var (
		accID        = assettest.CreateAccountFixture(ctx, t, nil, 0, nil)
		asset        = assettest.CreateAssetFixture(ctx, t, nil, 0, nil)
		_            = assettest.IssueAssetsFixture(ctx, t, asset, 2, accID)
		_            = assettest.IssueAssetsFixture(ctx, t, asset, 2, accID)
		assetAmount1 = &bc.AssetAmount{
			AssetID: asset,
			Amount:  1,
		}

		// An idempotency key that both reservations should use.
		clientToken1 = "a-unique-idempotency-key"
		clientToken2 = "another-unique-idempotency-key"
		wantSrc      = NewSource(ctx, assetAmount1, accID, nil, nil, &clientToken1)
		gotSrc       = NewSource(ctx, assetAmount1, accID, nil, nil, &clientToken1)
		separateSrc  = NewSource(ctx, assetAmount1, accID, nil, nil, &clientToken2)
	)

	reserveFunc := func(source *txbuilder.Source) *txbuilder.ReserveResult {
		got, err := source.Reserver.Reserve(ctx, assetAmount1, time.Minute)
		if err != nil {
			t.Log(errors.Stack(err))
			t.Fatal(err)
		}
		if len(got.Items) != 1 {
			t.Fatalf("expected 1 result utxo")
		}
		// generated address can change based on test ordering, so ignore in comparison
		got.Items[0].TemplateInput = nil

		ar, ok := got.Change[0].Receiver.(*Receiver)
		if !ok {
			t.Fatalf("expected change destination to have Receiver")
		}
		if ar.AccountID() != accID {
			t.Errorf("got receiver addr account = %v want %v", ar.AccountID(), accID)
		}
		// clear out to not compare generated addresses
		got.Change[0].Receiver = nil
		return got
	}

	var (
		got      = reserveFunc(gotSrc)
		want     = reserveFunc(wantSrc)
		separate = reserveFunc(separateSrc)
	)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reserve result\ngot:\n\t%+v\nwant:\n\t%+v", got, want)
		t.Errorf("reserve item\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0], want.Items[0])
		t.Errorf("reserve txin\ngot:\n\t%+v\nwant:\n\t%+v", got.Items[0].TxInput, want.Items[0].TxInput)
		t.Errorf("reserve change\ngot:\n\t%+v\nwant:\n\t%+v", got.Change, want.Change)
	}

	// The third reservation attempt should be distinct and not the same as the first two.
	if reflect.DeepEqual(separate, want) {
		t.Errorf("reserve result\ngot:\n\t%+v\ndo not want:\n\t%+v", separate, want)
		t.Errorf("reserve item\ngot:\n\t%+v\ndo not want:\n\t%+v", separate.Items[0], want.Items[0])
		t.Errorf("reserve txin\ngot:\n\t%+v\ndo not want:\n\t%+v", separate.Items[0].TxInput, want.Items[0].TxInput)
		t.Errorf("reserve change\ngot:\n\t%+v\ndo not want:\n\t%+v", separate.Change, want.Change)
	}
}

func TestAccountSourceWithTxHash(t *testing.T) {
	ctx := pg.NewContext(context.Background(), pgtest.NewTx(t))
	_, _, err := assettest.InitializeSigningGenerator(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var (
		acc      = assettest.CreateAccountFixture(ctx, t, nil, 0, nil)
		asset    = assettest.CreateAssetFixture(ctx, t, nil, 0, nil)
		assetAmt = bc.AssetAmount{AssetID: asset, Amount: 1}
		utxos    = 4
		srcTxs   []bc.Hash
	)

	for i := 0; i < utxos; i++ {
		o := assettest.IssueAssetsFixture(ctx, t, asset, 1, acc)
		srcTxs = append(srcTxs, o.Outpoint.Hash)
	}

	for i := 0; i < utxos; i++ {
		theTxHash := srcTxs[i]
		source := NewSource(ctx, &assetAmt, acc, &theTxHash, nil, nil)

		gotRes, err := source.Reserver.Reserve(ctx, &assetAmt, time.Minute)
		if err != nil {
			t.Log(errors.Stack(err))
			t.Fatal(err)
		}

		if len(gotRes.Items) != 1 {
			t.Fatalf("expected 1 result utxo")
		}

		got := gotRes.Items[0].TxInput.Outpoint()
		want := bc.Outpoint{Hash: theTxHash, Index: 0}
		if got != want {
			t.Errorf("reserved utxo outpoint got=%v want=%v", got, want)
		}
	}
}