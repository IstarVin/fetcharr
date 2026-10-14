package dev.istarvin.fetcharr.notifications

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import dev.istarvin.fetcharr.data.sync.ActionOutcome
import dev.istarvin.fetcharr.data.sync.JobActions
import dev.istarvin.fetcharr.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** The Retry action of a result notification: retries one job or all failed episodes of a batch. */
class RetryReceiver : BroadcastReceiver() {
    /**
     * Pulled by hand: with Kotlin, `@AndroidEntryPoint` on a receiver fails to compile because its generated base
     * class is only wired in by bytecode transformation, and `super.onReceive` is abstract at compile time.
     */
    @EntryPoint
    @InstallIn(SingletonComponent::class)
    interface Dependencies {
        fun actions(): JobActions

        fun notifier(): SystemNotifier

        fun submitListener(): SubmitListener

        @ApplicationScope
        fun scope(): CoroutineScope
    }

    override fun onReceive(context: Context, intent: Intent) {
        val deps = EntryPointAccessors.fromApplication(context.applicationContext, Dependencies::class.java)
        val actions = deps.actions()
        val notifier = deps.notifier()
        val submitListener = deps.submitListener()
        val scope = deps.scope()
        val key = intent.getStringExtra(NotificationIntents.EXTRA_NOTIFICATION_KEY)
        val jobId = intent.getStringExtra(NotificationIntents.EXTRA_JOB_ID)
        val batchId = intent.getStringExtra(NotificationIntents.EXTRA_BATCH_ID)
        if (jobId == null && batchId == null) return
        val pending = goAsync()
        scope.launch {
            try {
                // A broadcast may only run for about ten seconds.
                val outcome = withTimeoutOrNull(RECEIVER_TIMEOUT_MS) {
                    if (jobId != null) actions.retry(jobId) else actions.retryAll(batchId.orEmpty())
                }
                when (outcome) {
                    ActionOutcome.Ok -> {
                        if (key != null) notifier.cancelResult(key)
                        submitListener.onSubmitSucceeded()
                    }
                    // The job moved on already; the Retry button would be stale.
                    is ActionOutcome.Changed -> if (key != null) notifier.cancelResult(key)
                    else -> Unit // keep the notification so the user can try again
                }
            } finally {
                pending.finish()
            }
        }
    }

    private companion object {
        const val RECEIVER_TIMEOUT_MS = 9_000L
    }
}
