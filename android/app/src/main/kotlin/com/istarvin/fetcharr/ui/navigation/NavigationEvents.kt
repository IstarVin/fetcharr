package com.istarvin.fetcharr.ui.navigation

import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.receiveAsFlow
import javax.inject.Inject
import javax.inject.Singleton

/** A tab a notification asks the app to open. */
enum class OpenTarget(val extraValue: String) {
    QUEUE("queue"),
    HISTORY("history"),
    ;

    companion object {
        /** The `open` intent extra of a notification tap; null for anything else. */
        fun fromExtra(value: String?): OpenTarget? = entries.firstOrNull { it.extraValue == value }
    }
}

/**
 * "Open this tab" requests from notification taps. Buffered, so a tap that launches the app is delivered once the
 * root UI collects. Collect from one place only (the root UI).
 */
@Singleton
class NavigationEvents @Inject constructor() {
    private val channel = Channel<OpenTarget>(Channel.UNLIMITED)

    val events: Flow<OpenTarget> = channel.receiveAsFlow()

    fun open(target: OpenTarget) {
        channel.trySend(target)
    }
}
