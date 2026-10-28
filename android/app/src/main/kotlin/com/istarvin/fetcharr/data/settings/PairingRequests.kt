package com.istarvin.fetcharr.data.settings

import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** A server offered by a `fetcharr://connect` link. Kept in memory until the user accepts or dismisses it. */
data class PairingRequest(val url: String, val token: String)

/** Holds the latest pairing link until Settings shows it. Never persisted or logged: it may contain a token. */
@Singleton
class PairingRequests @Inject constructor() {
    private val _pending = MutableStateFlow<PairingRequest?>(null)
    val pending: StateFlow<PairingRequest?> = _pending.asStateFlow()

    fun offer(request: PairingRequest) {
        _pending.value = request
    }

    fun clear() {
        _pending.value = null
    }
}
