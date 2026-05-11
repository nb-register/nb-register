package com.nbregister.whatsappforwarder.service

import android.app.Notification
import android.os.Build
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.util.Log
import com.nbregister.whatsappforwarder.data.OtpExtractor
import com.nbregister.whatsappforwarder.network.OtpWebhookClient
import com.nbregister.whatsappforwarder.settings.SettingsStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

class WhatsAppNotificationListenerService : NotificationListenerService() {
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val client = OtpWebhookClient()

    override fun onNotificationPosted(sbn: StatusBarNotification?) {
        val item = sbn ?: return
        val settings = SettingsStore(applicationContext)
        val appSettings = settings.readAll()
        if (
            appSettings.webhookUrl.isBlank() ||
            item.packageName !in SettingsStore.WATCHED_PACKAGES
        ) {
            return
        }

        val appName = resolveAppName(item.packageName)
        val candidates = extractCandidates(item.notification)
        if (candidates.isEmpty()) {
            return
        }

        serviceScope.launch {
            for (candidate in candidates) {
                val otp = OtpExtractor.extractOtp(candidate.text) ?: continue
                if (!OtpExtractor.hasKeyword(candidate.text, SettingsStore.OTP_KEYWORDS)) {
                    continue
                }

                val result = client.send(appSettings.webhookUrl, otp)
                if (result.success) {
                    Log.i(TAG, "Forwarded WhatsApp OTP from $appName")
                } else {
                    Log.w(TAG, "Failed to forward WhatsApp OTP: ${result.message}")
                }
            }
        }
    }

    override fun onDestroy() {
        serviceScope.cancel()
        super.onDestroy()
    }

    private fun resolveAppName(packageName: String): String {
        return runCatching {
            val appInfo = packageManager.getApplicationInfo(packageName, 0)
            packageManager.getApplicationLabel(appInfo).toString()
        }.getOrDefault(packageName)
    }

    @Suppress("DEPRECATION")
    private fun extractCandidates(notification: Notification): List<MessageCandidate> {
        val extras = notification.extras ?: return emptyList()
        val title = extras.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
        val subText = extras.getCharSequence(Notification.EXTRA_SUB_TEXT)?.toString().orEmpty()
        val summary = extras.getCharSequence(Notification.EXTRA_SUMMARY_TEXT)?.toString().orEmpty()
        val candidates = linkedSetOf<MessageCandidate>()

        fun add(candidateTitle: String, body: CharSequence?) {
            val text = body?.toString()?.trim().orEmpty()
            if (text.isBlank()) {
                return
            }
            val mergedTitle = candidateTitle.ifBlank { title }.trim()
            val mergedText = listOf(mergedTitle, subText, summary, text)
                .filter { it.isNotBlank() }
                .joinToString("\n")
            candidates += MessageCandidate(title = mergedTitle, text = mergedText)
        }

        add(title, extras.getCharSequence(Notification.EXTRA_TEXT))
        add(title, extras.getCharSequence(Notification.EXTRA_BIG_TEXT))
        extras.getCharSequenceArray(Notification.EXTRA_TEXT_LINES)
            ?.forEach { line -> add(title, line) }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            val messages = Notification.MessagingStyle.Message.getMessagesFromBundleArray(
                extras.getParcelableArray(Notification.EXTRA_MESSAGES),
            )
            messages.forEach { message ->
                val sender = message.sender?.toString().orEmpty()
                add(sender.ifBlank { title }, message.text)
            }
        }

        return candidates.toList()
    }

    private data class MessageCandidate(
        val title: String,
        val text: String,
    )

    companion object {
        private const val TAG = "WhatsAppForwarder"
    }
}
