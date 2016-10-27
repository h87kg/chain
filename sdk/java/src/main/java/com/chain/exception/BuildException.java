package com.chain.exception;

import java.util.List;
import com.google.gson.annotations.SerializedName;

/**
 * BuildException wraps errors returned by the build-transaction endpoint.
 */
public class BuildException extends APIException {

    public BuildException(String message, String requestId) {
        super(message, requestId);
    }

    public static class ActionError extends APIException {
        
        public ActionError(String message, String requestId) {
            super(message, requestId);
        }

        /**
         * The index of the action that this error pertains to.
         */
        @SerializedName("data")
        public int index;
    }

    /**
     * A list of errors resulting from building actions.
     */
    @SerializedName("data")
    public List<ActionError> actionErrors;
}
