package com.istarvin.fetcharr.ui.navigation

import kotlinx.serialization.Serializable

@Serializable
data object QueueRoute

@Serializable
data object HistoryRoute

@Serializable
data object SettingsRoute

@Serializable
data object ServerSettingsRoute

/** @property url a URL to pre-fill, for example from the share sheet. */
@Serializable
data class AddRoute(val url: String? = null)
