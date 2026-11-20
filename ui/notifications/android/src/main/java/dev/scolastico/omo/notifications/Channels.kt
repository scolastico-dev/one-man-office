package dev.scolastico.omo.notifications

import android.Manifest
import android.app.*
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.net.Uri
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject

object Channels {
    const val EVENTS="omo-board-notifications"
    const val SERVICE="omo-polling-service"
    const val SERVICE_ID=1
    fun create(context: Context) {
        if(Build.VERSION.SDK_INT>=26) {
            val manager=context.getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(NotificationChannel(EVENTS,"Board notifications",NotificationManager.IMPORTANCE_DEFAULT))
            manager.createNotificationChannel(NotificationChannel(SERVICE,"Foreground polling",NotificationManager.IMPORTANCE_LOW))
        }
    }
    fun allowed(context: Context, mode:String):Boolean {
        if(Build.VERSION.SDK_INT>=33 && ContextCompat.checkSelfPermission(context,Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED) return false
        if(!NotificationManagerCompat.from(context).areNotificationsEnabled()) return false
        if(Build.VERSION.SDK_INT>=26) {
            val manager=context.getSystemService(NotificationManager::class.java)
            for(id in if(mode=="foreground") listOf(EVENTS,SERVICE) else listOf(EVENTS)) {
                val channel=manager.getNotificationChannel(id) ?: return false
                if(channel.importance==NotificationManager.IMPORTANCE_NONE) return false
            }
        }
        return true
    }
    private fun intent(context:Context,work:WorkConfig,path:String,id:String):PendingIntent {
        val intent=context.packageManager.getLaunchIntentForPackage(context.packageName)
            ?: Intent().setClassName(context.packageName,"dev.scolastico.omo.ui.MainActivity")
        intent.action="dev.scolastico.omo.NOTIFICATION"
        intent.data=Uri.Builder().scheme("omo-notification").authority(work.generation).appendPath(id).build()
        intent.putExtra("connection",work.id).putExtra("generation",work.generation).putExtra("destination",path)
        intent.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(context,0,intent,PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
    }
    fun service(context:Context):Notification = NotificationCompat.Builder(context,SERVICE)
        .setSmallIcon(android.R.drawable.ic_popup_sync).setContentTitle("omo ui is checking notifications")
        .setContentText("Foreground mode · open omo ui to change or stop polling")
        .setOngoing(true).setSilent(true).setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        .setContentIntent(context.packageManager.getLaunchIntentForPackage(context.packageName)?.let {
            PendingIntent.getActivity(context,1,it,PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        }).build()
    fun deliver(context:Context,work:WorkConfig,notice:JSONObject):Boolean {
        if(!allowed(context,work.mode)) return false
        val notification=NotificationCompat.Builder(context,EVENTS).setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(notice.getString("title")).setContentText(notice.getString("body"))
            .setStyle(NotificationCompat.BigTextStyle().bigText(notice.getString("body")))
            .setAutoCancel(true).setOnlyAlertOnce(true)
            .setContentIntent(intent(context,work,notice.getString("destination"),notice.getString("id"))).build()
        // Stable tag replaces a replayed event after a crash without a second alert.
        context.getSystemService(NotificationManager::class.java).notify("${work.id}:${notice.getString("id")}",2,notification)
        return true
    }
}
