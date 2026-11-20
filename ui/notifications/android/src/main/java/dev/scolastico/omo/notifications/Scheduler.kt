package dev.scolastico.omo.notifications

import android.content.Context
import android.content.Intent
import androidx.core.content.ContextCompat
import androidx.work.*
import java.util.concurrent.TimeUnit

class Scheduler(private val context:Context) {
    companion object {const val WORK="omo-notification-feed"}
    fun cancel() {
        WorkManager.getInstance(context).cancelUniqueWork(WORK).result.get()
        context.stopService(Intent(context,PollingService::class.java))
    }
    fun start(work:WorkConfig) {
        if(work.mode=="background") {
            val request=PeriodicWorkRequestBuilder<PollingWorker>(15,TimeUnit.MINUTES)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setInputData(workDataOf("generation" to work.generation)).build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK,ExistingPeriodicWorkPolicy.CANCEL_AND_REENQUEUE,request).result.get()
        } else {
            ContextCompat.startForegroundService(context,Intent(context,PollingService::class.java).putExtra("generation",work.generation))
        }
    }
}
class PollingWorker @JvmOverloads constructor(context:Context, params:WorkerParameters, private val engines:(Context)->PollingEngine = RuntimePolling::engine): Worker(context,params) {
    override fun doWork():Result {
        val generation=inputData.getString("generation") ?: return Result.success()
        return try {
            val engine=engines(applicationContext)
            // A finite batch avoids occupying the background worker indefinitely.
            repeat(20) {if(isStopped || !engine.step(generation,"background")) return Result.success()}
            Result.success()
        } catch(e:Exception) {Result.retry()}
    }
}
