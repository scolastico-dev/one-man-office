package dev.scolastico.omo.notifications

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import org.json.JSONObject
import java.io.File
import java.io.FileNotFoundException
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// The work DB and intents contain only a random generation. The private work
// configuration (including Authorization material) is encrypted with Android Keystore.
data class WorkConfig(val config: JSONObject, val secret: String, val root: String,
    val generation: String, var error: String = "", var recovery: JSONObject? = null, val enabled: Boolean = true) {
    val id: String get() = config.getString("id")
    val mode: String get() = config.getString("notification_mode")
    fun json(): JSONObject = JSONObject().put("config",config).put("secret",secret)
        .put("root",root).put("generation",generation).put("error",error).put("recovery",recovery).put("enabled",enabled)
    companion object {
        fun parse(v:JSONObject) = WorkConfig(v.getJSONObject("config"),v.getString("secret"),
            v.getString("root"),v.getString("generation"),v.optString("error"),v.optJSONObject("recovery"),v.optBoolean("enabled",true))
    }
}
interface WorkStore {
    fun load(): WorkConfig?
    fun save(work: WorkConfig?)
}
class EncryptedWorkStore(context: Context, private val encryptionKey: (() -> SecretKey)? = null): WorkStore {
    private val file = AtomicFile(File(context.noBackupFilesDir,"notification-work"))
    private fun key(): SecretKey {
        encryptionKey?.let {return it()}
        val store=KeyStore.getInstance("AndroidKeyStore").apply {load(null)}
        val alias="omo-ui-notification-work"
        (store.getKey(alias,null) as? SecretKey)?.let {return it}
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES,"AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(alias,KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        }.generateKey()
    }
    override fun load(): WorkConfig? {
        val bytes=try { file.readFully() } catch(missing:FileNotFoundException) {
            // openRead recovers a committed .bak before opening the base. Never
            // short-circuit that recovery, including on Android's legacy writer.
            // FileNotFoundException also represents denied access: only ENOENT
            // for both committed paths means the connection is actually absent.
            for(candidate in listOf(file.baseFile,File(file.baseFile.path + ".bak"))) {
                try {
                    Os.stat(candidate.path)
                    throw missing
                } catch(stat:ErrnoException) {
                    if(stat.errno!=OsConstants.ENOENT) throw missing
                }
            }
            return null
        }
        require(bytes.size>12)
        val cipher=Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE,key(),GCMParameterSpec(128,bytes.copyOfRange(0,12)))
        return WorkConfig.parse(JSONObject(String(cipher.doFinal(bytes.copyOfRange(12,bytes.size)),Charsets.UTF_8)))
    }
    override fun save(work:WorkConfig?) {
        if(work==null) {file.delete();return}
        val cipher=Cipher.getInstance("AES/GCM/NoPadding").apply {init(Cipher.ENCRYPT_MODE,key())}
        val bytes=cipher.iv + cipher.doFinal(work.json().toString().toByteArray(Charsets.UTF_8))
        val stream=file.startWrite()
        try {stream.write(bytes);file.finishWrite(stream)} catch(e:Exception) {file.failWrite(stream);throw e}
    }
}
