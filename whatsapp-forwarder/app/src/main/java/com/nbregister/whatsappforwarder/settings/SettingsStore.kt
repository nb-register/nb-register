package com.nbregister.whatsappforwarder.settings

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit
import com.nbregister.whatsappforwarder.BuildConfig

data class AppSettings(
    val webhookUrl: String,
)

class SettingsStore(context: Context) {
    private val prefs: SharedPreferences =
        context.applicationContext.getSharedPreferences("whatsapp_forwarder_settings", Context.MODE_PRIVATE)

    var webhookUrl: String
        get() = prefs.getString(KEY_WEBHOOK_URL, BuildConfig.DEFAULT_WEBHOOK_URL) ?: ""
        set(value) = prefs.edit { putString(KEY_WEBHOOK_URL, value.trim()) }

    fun readAll(): AppSettings {
        return AppSettings(
            webhookUrl = webhookUrl,
        )
    }

    companion object {
        val WATCHED_PACKAGES = setOf("com.whatsapp", "com.whatsapp.w4b")
        val OTP_KEYWORDS = setOf(
            "otp",
            "code",
            "kode",
            "verification",
            "verifikasi",
            "gopay",
            "gojek",
            "one-time",
            "sekali pakai",
        )

        private const val KEY_WEBHOOK_URL = "webhook_url"
    }
}
