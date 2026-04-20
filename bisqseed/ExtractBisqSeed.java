import org.bitcoinj.crypto.KeyCrypterException;
import org.bitcoinj.wallet.DeterministicSeed;
import org.bitcoinj.wallet.UnreadableWalletException;
import org.bitcoinj.wallet.Wallet;

import java.io.File;
import java.util.List;

public final class ExtractBisqSeed {
    public static void main(String[] args) throws Exception {
        if (args.length < 1 || args.length > 2) {
            System.err.println("usage: ExtractBisqSeed <wallet-file> [wallet-password]");
            System.exit(2);
        }

        File walletFile = new File(args[0]);
        if (!walletFile.isFile()) {
            System.err.println("wallet file not found: " + walletFile);
            System.exit(2);
        }

        String walletPassword = args.length == 2 ? args[1] : "";
        Wallet wallet = loadWallet(walletFile);
        DeterministicSeed seed = wallet.getKeyChainSeed();
        if (seed == null) {
            throw new IllegalStateException("wallet has no deterministic seed");
        }

        if (seed.isEncrypted()) {
            if (walletPassword == null || walletPassword.isEmpty()) {
                throw new IllegalStateException("wallet seed is encrypted but no wallet password was provided");
            }
            if (wallet.getKeyCrypter() == null) {
                throw new IllegalStateException("wallet seed is encrypted but no key crypter is available");
            }
            var aesKey = wallet.getKeyCrypter().deriveKey(walletPassword);
            try {
                seed = seed.decrypt(wallet.getKeyCrypter(), walletPassword, aesKey);
            } catch (KeyCrypterException e) {
                throw new IllegalStateException("failed to decrypt wallet seed", e);
            }
        }

        List<String> mnemonicWords = seed.getMnemonicCode();
        if (mnemonicWords == null || mnemonicWords.isEmpty()) {
            throw new IllegalStateException("wallet mnemonic is empty");
        }
        String mnemonic = String.join(" ", mnemonicWords).trim();

        long creationTimeSeconds = seed.getCreationTimeSeconds();
        System.out.println(mnemonic);
        if (creationTimeSeconds > 0) {
            System.out.println(java.time.Instant.ofEpochSecond(creationTimeSeconds));
        } else {
            System.out.println();
        }
    }

    private static Wallet loadWallet(File walletFile) throws UnreadableWalletException {
        try {
            return Wallet.loadFromFile(walletFile);
        } catch (UnreadableWalletException e) {
            throw e;
        } catch (RuntimeException e) {
            throw e;
        }
    }
}
