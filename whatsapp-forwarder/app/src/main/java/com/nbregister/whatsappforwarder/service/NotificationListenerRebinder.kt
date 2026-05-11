package com.nbregister.whatsappforwarder.service

import android.content.ComponentName
import android.content.Context
import android.service.notification.NotificationListenerService
import android.util.Log

object NotificationListenerRebinder {
    fun request(context: Context) {
        val component = ComponentName(
            context.applicationContext,
            WhatsAppNotificationListenerService::class.java,
        )
        runCatching {
            NotificationListenerService.requestRebind(component)
        }.onFailure { exc ->
            Log.w(TAG, "Failed to request notification listener rebind: ${exc.message}")
        }
    }

    private const val TAG = "WhatsAppForwarder"
}
