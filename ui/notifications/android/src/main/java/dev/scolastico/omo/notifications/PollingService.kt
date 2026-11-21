package dev.scolastico.omo.notifications

import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

open class PollingService(
    private val engines: (Context) -> PollingEngine = RuntimePolling::engine,
    private val executor: ExecutorService = Executors.newSingleThreadExecutor(),
    private val promote: (Service) -> Unit = { service ->
        if (Build.VERSION.SDK_INT >= 29) service.startForeground(Channels.SERVICE_ID,
            Channels.service(service), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        else service.startForeground(Channels.SERVICE_ID, Channels.service(service))
    }
): Service() {
    private val handler = Handler(Looper.getMainLooper())
    @Volatile private var running = false
    // Changed only on the main thread. Every queued callback captures its start.
    @Volatile private var epoch = 0L
    @Volatile private var generation = ""
    override fun onBind(intent: Intent?): IBinder? = null

    private fun current(run: Long) = running && epoch == run
    private fun stop(run: Long, id: String, reason: String? = null) {
        val finish = Runnable {
            if (epoch != run) return@Runnable
            running = false
            epoch++
            handler.removeCallbacksAndMessages(null)
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            if (reason != null) {
                // A bad store must never prevent stopping within Android's deadline.
                try { executor.execute {
                    try { engines(this).serviceStopped(id, reason) } catch (_: Exception) { }
                } } catch (_: Exception) { }
            }
        }
        if (Looper.myLooper() == Looper.getMainLooper()) finish.run() else handler.post(finish)
    }
    private fun submit(run: Long, id: String, action: (PollingEngine) -> Unit) {
        if (!current(run)) return
        try { executor.execute {
            if (!current(run)) return@execute
            try { action(engines(this)) }
            catch (_: Exception) { stop(run, id, "Foreground polling is unavailable. Reopen Connection and check saved credentials and Android notification settings.") }
            catch (_: LinkageError) { stop(run, id, "Foreground polling could not load its native feed. Reinstall omo ui or select Background mode.") }
        } } catch (_: Exception) {
            stop(run, id, "Foreground polling stopped. Reopen Connection to restart it.")
        }
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val run = ++epoch
        val requested = intent?.getStringExtra("generation")
        generation = requested ?: ""
        running = true
        handler.removeCallbacksAndMessages(null)
        try {
            Channels.create(this)
            // Promote before disk/HTTP work and catch asynchronous OS rejection here.
            promote(this)
        } catch (_: Exception) {
            stop(run, generation, "Android could not start foreground polling. Keep omo ui open, check notification settings, then reconnect or select Background mode.")
            return START_NOT_STICKY
        }
        submit(run, generation) { engine ->
            val work = engine.current()
            if (work == null || !work.enabled || work.mode != "foreground" ||
                (requested != null && work.generation != requested)) {
                stop(run, requested ?: "")
                return@submit
            }
            generation = work.generation
            handler.post(object: Runnable {
                override fun run() {
                    submit(run, work.generation) { currentEngine ->
                        val more = currentEngine.step(work.generation, "foreground")
                        if (currentEngine.current()?.generation != work.generation) {
                            stop(run, work.generation)
                        } else if (current(run)) {
                            handler.postDelayed(this, if (more) 0L else 5000L)
                        }
                    }
                }
            })
        }
        return START_STICKY
    }
    override fun onTimeout(startId: Int, fgsType: Int) {
        stop(epoch, generation, "Android reached the foreground data-sync time limit. Reopen Connection to restart foreground polling or select Background mode.")
    }
    override fun onDestroy() {
        running = false
        epoch++
        handler.removeCallbacksAndMessages(null)
        stopForeground(STOP_FOREGROUND_REMOVE)
        executor.shutdown()
        super.onDestroy()
    }
}
