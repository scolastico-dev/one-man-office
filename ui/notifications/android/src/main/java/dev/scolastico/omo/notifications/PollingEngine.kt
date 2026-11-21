package dev.scolastico.omo.notifications

import android.content.Context
import android.content.Intent
import org.json.JSONObject
import java.util.UUID

interface Feed {
    fun baseline(work: WorkConfig): JSONObject = JSONObject()
    fun poll(work: WorkConfig, deliver: (JSONObject) -> Boolean): JSONObject
    fun recover(work: WorkConfig, replay: Boolean): JSONObject
}
class NativeFeed(private val context: Context): Feed {
    init { System.loadLibrary("omo_ui") }
    private external fun pollFeed(config: String, root: String, secret: String, sink: DeliverySink, baseline: Boolean): String
    private external fun recoverFeed(config: String, root: String, recovery: String, replay: Boolean): String
    override fun baseline(work: WorkConfig) = if(work.config.optString("mode")=="relay") JSONObject()
        else JSONObject(pollFeed(work.config.toString(),work.root,work.secret,DeliverySink({false}),true))
    override fun poll(work: WorkConfig, deliver: (JSONObject) -> Boolean) =
        JSONObject(pollFeed(work.config.toString(),work.root,work.secret,DeliverySink(deliver,if(work.config.optString("mode")=="relay") EncryptedRelayJournal(context,work.id) else null),false))
    override fun recover(work: WorkConfig, replay: Boolean) = JSONObject(recoverFeed(work.config.toString(),work.root,work.recovery!!.toString(),replay))
}
class DeliverySink(private val submit: (JSONObject) -> Boolean, private val journal: EncryptedRelayJournal? = null) {
    // JNI performs receipt/notice persistence before a later GET can acknowledge.
    fun loadJournal(): String = requireNotNull(journal).load()
    fun saveJournal(value: String): Boolean { requireNotNull(journal).save(value); return true }
    // OS submission completion is journaled synchronously after delivery.
    fun deliver(json: String): Boolean = submit(JSONObject(json))
}
class PollingEngine(private val context: Context, private val store: WorkStore, private val feed: Feed) {
    // A single process hosts the service, WorkManager and plugin. Hold this monitor
    // through delivery/persistence so a completed disconnect cannot race an old page.
    companion object { private val lock=Any() }
    private val scheduler=Scheduler(context)
    @Volatile private var failure=""
    fun configure(args: JSONObject?) = synchronized(lock) {
        val config=args?.getJSONObject("config")
        val enabled=config!=null && config.optBoolean("notifications") && args.optBoolean("enabled",true)
        val next=if(config!=null) {
            require(config.optString("notification_mode") in setOf("background","foreground"))
            if(enabled) {
                require(config.optString("auth")=="none" || config.optBoolean("remember"))
                require(Channels.allowed(context,config.getString("notification_mode"))) {"Android notifications or a notification channel are disabled."}
            }
            val secret=if(config.optBoolean("remember")) args!!.getString("secret") else ""
            WorkConfig(config,secret,args!!.getString("root"),UUID.randomUUID().toString(),enabled=enabled)
        } else null
        // App startup must not erase the saved work while a cold notification
        // intent is being validated. An explicit reconnect still restarts work.
        if(args?.optBoolean("resume")==true && next!=null) {
            val previous=store.load()
            fun fields(config: JSONObject) = config.keys().asSequence().associateWith {config.get(it)}
            if(previous!=null && fields(previous.config)==fields(next.config) &&
                previous.secret==next.secret && previous.root==next.root && previous.enabled==next.enabled) return@synchronized
        }
        // Durable invalidation precedes cancellation; an already executing old job
        // will observe a different generation even if Android delivers it later.
        store.save(null)
        scheduler.cancel()
        failure=""
        if(next!=null) {
            val baseline=if(next.enabled) feed.baseline(next) else JSONObject()
            next.error=baseline.optString("error")
            next.recovery=baseline.optJSONObject("recovery")
            store.save(next)
            try { if(next.enabled) scheduler.start(next) } catch(e: Exception) {
                store.save(null)
                scheduler.cancel()
                throw e
            }
        }
    }
    fun current(): WorkConfig? = synchronized(lock) {store.load()}
    fun status(): JSONObject = synchronized(lock) {
        val work=try {store.load()} catch(e:Exception) {
            return JSONObject().put("connected",false).put("polling",false)
                .put("error",failure.ifEmpty {"Android could not read notification settings. Reconnect and check saved credentials."})
        }
        JSONObject().put("connected",work!=null).put("polling",work?.enabled ?: false)
            .put("error",work?.error?.ifEmpty {failure} ?: failure).put("recovery",work?.recovery)
    }
    fun step(generation: String, mode: String): Boolean = synchronized(lock) {
        val work=store.load()?.takeIf {it.enabled && it.generation==generation && it.mode==mode} ?: return false
        if(!Channels.allowed(context,mode)) {
            work.error="Enable Android notifications and the notification channels in Android Settings."
            store.save(work);return false
        }
        if(work.recovery!=null) return false
        // Bound each worker invocation; leave remaining pages for the next run.
        // The foreground loop drains immediately while there is more history.
        val reply=try {feed.poll(work) { Channels.deliver(context,work,it) }} catch(e: Exception) {
            JSONObject().put("error","Android could not poll the board. Check the connection and saved credentials.")
        }
        work.error=reply.optString("error")
        work.recovery=reply.optJSONObject("recovery")
        store.save(work)
        reply.optBoolean("more")
    }
    fun recover(replay: Boolean) = synchronized(lock) {
        val work=store.load() ?: error("Connect first.")
        require(work.recovery!=null) {"No cursor recovery is pending."}
        val reply=feed.recover(work,replay)
        require(!reply.has("error")) {reply.optString("error")}
        work.recovery=null;work.error="";store.save(work)
    }
    fun serviceStopped(generation: String, reason: String) = synchronized(lock) {
        val work=try {store.load()} catch(e:Exception) {failure=reason;return}
        work?.takeIf {it.enabled && it.mode=="foreground" && (generation.isEmpty() || it.generation==generation)}?.let {
            it.error=reason
            try {store.save(it)} catch(e:Exception) {failure=reason}
        }
        Unit
    }
    fun activation(intent: Intent): JSONObject? = synchronized(lock) {
        if(intent.action!="dev.scolastico.omo.NOTIFICATION") return null
        val work=store.load() ?: return null
        if(intent.getStringExtra("connection")!=work.id) return null
        val path=intent.getStringExtra("destination") ?: return null
        // Rust validates the board-relative destination again before navigation.
        JSONObject().put("connection",work.id).put("destination",path)
    }
}
object RuntimePolling {
    private var instance: PollingEngine?=null
    @Synchronized fun engine(context: Context): PollingEngine = instance ?: PollingEngine(
        context.applicationContext,EncryptedWorkStore(context.applicationContext),NativeFeed(context.applicationContext)
    ).also { instance=it }
}
