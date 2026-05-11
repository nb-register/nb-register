package com.nbregister.whatsappforwarder.worker

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.Worker
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.nbregister.whatsappforwarder.network.OtpWebhookClient
import com.nbregister.whatsappforwarder.settings.SettingsStore
import java.util.concurrent.TimeUnit

class OtpForwardWorker(
    appContext: Context,
    workerParams: WorkerParameters,
) : Worker(appContext, workerParams) {
    override fun doWork(): Result {
        val otp = inputData.getString(KEY_OTP).orEmpty()
        if (otp.isBlank()) {
            return Result.failure()
        }

        val webhookUrl = inputData.getString(KEY_WEBHOOK_URL)
            ?.takeIf { it.isNotBlank() }
            ?: SettingsStore(applicationContext).webhookUrl

        if (webhookUrl.isBlank()) {
            return Result.retry()
        }

        val result = OtpWebhookClient().send(webhookUrl, otp)
        return when {
            result.success -> Result.success()
            result.permanentFailure -> Result.failure(workDataOf(KEY_ERROR to result.message))
            else -> Result.retry()
        }
    }

    companion object {
        private const val KEY_WEBHOOK_URL = "webhook_url"
        private const val KEY_OTP = "otp"
        private const val KEY_ERROR = "error"

        fun enqueue(context: Context, webhookUrl: String, otp: String) {
            val request = OneTimeWorkRequestBuilder<OtpForwardWorker>()
                .setConstraints(
                    Constraints.Builder()
                        .setRequiredNetworkType(NetworkType.CONNECTED)
                        .build(),
                )
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
                .setInputData(
                    workDataOf(
                        KEY_WEBHOOK_URL to webhookUrl,
                        KEY_OTP to otp,
                    ),
                )
                .build()

            WorkManager.getInstance(context.applicationContext).enqueue(request)
        }
    }
}
