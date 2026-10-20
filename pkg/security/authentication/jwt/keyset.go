package jwt

import (
	"crypto/rsa"
	"fmt"
	"math/big"

	"github.com/huynhanx03/go-common/pkg/security/authentication"
)

type KeySet struct {
	Active  map[string]*rsa.PublicKey
	Retired map[string]*rsa.PublicKey
}

type immutableKeySet struct {
	active  map[string]*rsa.PublicKey
	retired map[string]*rsa.PublicKey
}

func copyKeySet(input KeySet) (immutableKeySet, error) {
	if len(input.Active) == 0 {
		return immutableKeySet{}, fmt.Errorf("%w: no active keys", authentication.ErrInvalidKeySet)
	}
	result := immutableKeySet{
		active:  make(map[string]*rsa.PublicKey, len(input.Active)),
		retired: make(map[string]*rsa.PublicKey, len(input.Retired)),
	}
	for keyID, publicKey := range input.Active {
		if err := validateKeyID(keyID); err != nil {
			return immutableKeySet{}, err
		}
		if err := validatePublicKey(publicKey); err != nil {
			return immutableKeySet{}, err
		}
		result.active[keyID] = clonePublicKey(publicKey)
	}
	for keyID, publicKey := range input.Retired {
		if err := validateKeyID(keyID); err != nil {
			return immutableKeySet{}, err
		}
		if _, duplicate := result.active[keyID]; duplicate {
			return immutableKeySet{}, fmt.Errorf(
				"%w: key ID appears in active and retired sets",
				authentication.ErrInvalidKeySet,
			)
		}
		if err := validatePublicKey(publicKey); err != nil {
			return immutableKeySet{}, err
		}
		result.retired[keyID] = clonePublicKey(publicKey)
	}
	return result, nil
}

func validatePublicKey(publicKey *rsa.PublicKey) error {
	if publicKey == nil || publicKey.N == nil || publicKey.N.BitLen() < 2048 || publicKey.E < 3 {
		return fmt.Errorf("%w: RSA key must be at least 2048 bits", authentication.ErrInvalidKeySet)
	}
	return nil
}

func clonePublicKey(publicKey *rsa.PublicKey) *rsa.PublicKey {
	return &rsa.PublicKey{
		N: new(big.Int).Set(publicKey.N),
		E: publicKey.E,
	}
}
