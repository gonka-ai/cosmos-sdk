# ECIES Encryption Integration

This change integrates ECIES (Elliptic Curve Integrated Encryption Scheme) with the Cosmos SDK keyring, using an implementation based on the Go Ethereum (Geth) library.

## Core Changes

1.  **New `crypto/ecies` Package**: A new package, `crypto/ecies`, has been added to the SDK. This package contains the core logic for ECIES encryption and decryption. It is a direct adaptation of the ECIES implementation found in Ethereum's Go client.

2.  **Keyring Integration (`crypto/keyring/keyring.go`)**: The main `keystore` was updated to support ECIES operations directly.
    *   A new `ECIESCrypto` interface has been defined and implemented, exposing `Encrypt`, `EncryptByAddress`, `Decrypt`, and `DecryptByAddress` methods.
    *   To make Cosmos SDK's `secp256k1` keys compatible with the standard cryptographic library used by the ECIES implementation, helper functions (`cosmosPrivKeyToECDSA` and `cosmosPubKeyToECDSA`) were introduced. These functions use the `decred/dcrd/dcrec/secp256k1` library to convert Cosmos keys into the standard `ecdsa` key format.

## Functional Details

The integration enables encrypting data with a recipient's public key, ensuring only the recipient can decrypt it with their private key.

This functionality is exposed through the keyring, allowing developers to perform ECIES operations using familiar key identifiers (`uid`) or addresses. The implementation handles the necessary key format conversions seamlessly in the background. This provides a robust layer of security for sensitive data that needs to be stored or transmitted. 