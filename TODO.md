# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* Accepting Help on a Hop that has been Canceled should not be an error- just a message that the Hop was canceled already.
* It is possible to get 'orphaned' offers to help. If the requesting User deletes your Offer message and doesn't respond, then you never get an answer...is that a problem?
* An Organization Owner can request membership in their own Organization- this should be prevented
* Don't show the "Remove" button on the row for the primary Organization Owner when they go to the Manage Organization page
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.
* The 403 unauthorized page says "This page is only available to organization owners." - need to make this more generic
* Deleting a User does not delete their Organization...what do we do here?
* When signing up with an existing email address we get the generic error "We could not process your request right now. Please try again.". We should say "That email address is already taken, please try another one."
* Move to a static tailwind CSS- don't pull dynamically
* Add a file size limit on org/user avatar pictures (2MB)


## Now

* Incorporate new UI Elements - images/colors
    * Adjust base template for graphic sidebar
    * Test on mobile device

* Set up incoming email accounts on hopshare.org

* /healthz endpoint should actually determine health- currently just returns 200

* Header

* My Profile
    * Need a way to remove an owned Organization- need to think a bit about this one- to ensure it doesn't get abused.
    * If the User is not primary owner of their own Organization, give them a button at bottom of "Organizations" tab that lets them create their own Organization. 

* Hop Detail Page
    * Summary section is a little clunky. We can probably tidy this up more...make easier to read

* My HopShare Dashboard
    * "Change Organization" Setting- should not be a Modal (lets get rid of all the modals btw)- make a new page showing pull down of existing Orgs you belong to, or button taking you to the Search Organizaitions page. Remove the "Manage" button- that should only be accessible from Profile now.

* Admin Page
    * Users
    * Organizations
        * We should let Admins 'inject' new Owners into an Organization if they get abandoned

* Organizations
    * Need to have a separate set of timebank parameters per organization
        * Minimum balance (default -5)
        * Maximum balance (default 10)
        * Starting balance (default 5)
    * The UI should enforce some sensible levels here to avoid crazy numbers that would make the timebank unusable.

* Joining an Organization should use messages
    * Send an information message to all Owners of an Organization when you request membership. The message body should contain a link that will take the Member directly to their 
    * Send yourself an information message that you requested membership in an Organization.

* Owners are moderators for listings- they can flag/delete inappropriate requests/comments

* Need an Organization-public Member page with more details about each member. Maybe have a way to send them a message?

* Change the "My Organization" panel of the "My Profile" page as follows:
    * In the list of Organizations the Member is associated with.
        * If the Member is not an Owner of the Organization, follow each Organization row with a placeholder link that says "Leave..." which we will eventually use to let a Member leave that Organization.

## Later

* Add in a REST API layer so we can eventually do a small mobile app.

* Put Hop image uploads behind a feature flag- we should not store them in the database- instead use reliable storage. Not needed right now.

* Make service/ExpireHelpRequests() asynchronous
    * Ideally we can run the 'hopshare' binary in a 'daemon' mode where it can do these sorts of things.
    * Coordinating async workers across processes is a pain- let's see what we can do with one process.

    * We also need a way to remove expired rows in the member_sessions table (e.g. user logs in, never does anything else).
    * Perhaps a more generic async 'worker' concept?

* Add in basic monitoring (cron job calling script saving in sqlite):
    * net/http/pprof package (visualize performance)
    * runtime.MemStats / runtime.ReadMemStats() thru a /health endpoint on each golang process
    * select count(*) from pg_stat_activity; (database connections)
    * iostat to see iops levels
    * jq against Caddy logs for traffic levels

* How can we use LLMs here? OpenClaw / Telegram idea from Sukumar


Font Awesome- https://icon-sets.iconify.design/fa7-regular/page-2.html?keyword=font


